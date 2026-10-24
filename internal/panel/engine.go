package panel

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cotten "cottendns-go/server"
	"github.com/getlantern/soar"
	masterdns "masterdnsvpn-go/server"
)

var (
	errAccountStreamLimit   = errors.New("stream limit reached")
	errServerStreamCapacity = errors.New("server stream capacity reached")
	errDestinationPort      = errors.New("destination port denied")
)

type packet struct {
	data []byte
	addr net.Addr
}
type virtualPacketConn struct {
	owner *Manager
	input chan packet
	done  chan struct{}
	once  sync.Once
}

func (p *virtualPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	select {
	case <-p.done:
		return 0, nil, net.ErrClosed
	case q := <-p.input:
		return copy(b, q.data), q.addr, nil
	}
}
func (p *virtualPacketConn) WriteTo(b []byte, a net.Addr) (int, error) {
	select {
	case <-p.done:
		return 0, net.ErrClosed
	default:
	}
	return p.owner.writeDNSUDP(b, a)
}
func (p *virtualPacketConn) Close() error                  { p.once.Do(func() { close(p.done) }); return nil }
func (p *virtualPacketConn) LocalAddr() net.Addr           { return p.owner.pc.LocalAddr() }
func (p *virtualPacketConn) SetDeadline(t time.Time) error { return p.SetReadDeadline(t) }
func (p *virtualPacketConn) SetReadDeadline(t time.Time) error {
	if !t.IsZero() {
		return errors.New("virtual DNS deadlines unsupported")
	}
	return nil
}
func (p *virtualPacketConn) SetWriteDeadline(t time.Time) error { return p.SetReadDeadline(t) }

type accountRuntime struct {
	masterMu       sync.Mutex
	master         *masterdns.Server
	soarPCs        map[string]*virtualPacketConn
	zones          []string
	cottenMu       sync.Mutex
	cotten         *cotten.Server
	client         Client
	ctx            context.Context
	cancel         context.CancelFunc
	pc             *virtualPacketConn
	mu             sync.Mutex
	closed         bool
	pending        int
	conns          map[*meterConn]struct{}
	rateMu         sync.Mutex
	rateHead       *paceTicket
	rateTail       *paceTicket
	resolverMu     sync.Mutex
	resolverHealth map[string]*resolverHealth
}

func (r *accountRuntime) closeIO() {
	r.mu.Lock()
	r.closed = true
	var conns []*meterConn
	for c := range r.conns {
		conns = append(conns, c)
	}
	r.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
}
func (r *accountRuntime) retire() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.cancel()
	r.pc.Close()
	for _, pc := range r.soarPCs {
		pc.Close()
	}
}
func (r *accountRuntime) stop() {
	r.retire()
	r.closeIO()
}

type Manager struct {
	domainProofKey [32]byte
	cancel         context.CancelFunc
	cottenJobs     chan cottenJob
	store          *Store
	pc             net.PacketConn
	ctx            context.Context
	mu             sync.RWMutex
	reconcileMu    sync.Mutex
	accounts       map[string]*accountRuntime
	zones          map[string]*accountRuntime
	cfg            Settings
	// dialOverride is supplied only by in-process integration tests. The shipped
	// command never supplies it and always applies public-destination protection.
	dialOverride          func(context.Context, string, string) (net.Conn, error)
	dnsIn, dnsOut, active atomic.Uint64
	started               time.Time
	workers               sync.WaitGroup
	diagnostics           runtimeCounters
}

func NewManager(ctx context.Context, s *Store, pc net.PacketConn) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{ctx: ctx, cancel: cancel, cottenJobs: make(chan cottenJob, 128), store: s, pc: pc, accounts: map[string]*accountRuntime{}, zones: map[string]*accountRuntime{}, started: time.Now()}
	rand.Read(m.domainProofKey[:])
	return m
}
func (m *Manager) Reconcile() error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	if m.ctx.Err() != nil {
		return nil
	}
	list, e := m.store.List()
	if e != nil {
		return e
	}
	cfg, e := m.store.Settings()
	if e != nil {
		return e
	}
	desired := map[string]Client{}
	for _, c := range list {
		if isInstallProbe(c) && c.Status(time.Now()) == "expired" {
			if err := m.store.RemoveInstallProbe(c.ID); err != nil {
				return err
			}
			continue
		}
		if c.Status(time.Now()) == "active" {
			desired[c.ID] = c
		}
	}
	var retired []*accountRuntime
	var create []Client
	m.mu.Lock()
	m.cfg = cfg
	for id, r := range m.accounts {
		c, ok := desired[id]
		if !ok || r.ctx.Err() != nil || c.Zone != r.client.Zone || c.PublicKey != r.client.PublicKey || c.Engines != r.client.Engines ||
			c.StreamLimit != r.client.StreamLimit || c.UsageEpoch != r.client.UsageEpoch ||
			c.SoarSessionLimit != r.client.SoarSessionLimit || c.CottenSessionLimit != r.client.CottenSessionLimit || c.MasterDNSSessionLimit != r.client.MasterDNSSessionLimit ||
			strings.Join(r.zones, ",") != strings.Join(cfg.accountZones(c), ",") {
			delete(m.accounts, id)
			for _, zone := range r.zones {
				delete(m.zones, zone)
			}
			r.retire()
			retired = append(retired, r)
		}
	}
	for id, c := range desired {
		if m.accounts[id] == nil {
			create = append(create, c)
		}
	}
	m.mu.Unlock()
	// Network Close and seed/database reads must never hold the shared routing
	// lock. Publish new runtimes before draining retired connections.
	defer func() {
		for _, r := range retired {
			r.closeIO()
		}
	}()
	for _, c := range create {
		seed, err := m.store.Seed(c.ID)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(m.ctx)
		v := &virtualPacketConn{owner: m, input: make(chan packet, 128), done: make(chan struct{})}
		rt := &accountRuntime{client: c, ctx: ctx, cancel: cancel, pc: v, conns: map[*meterConn]struct{}{}, zones: cfg.accountZones(c), soarPCs: map[string]*virtualPacketConn{}}
		for _, zone := range rt.zones {
			pc := v
			if zone != c.Zone {
				pc = &virtualPacketConn{owner: m, input: make(chan packet, 128), done: make(chan struct{})}
			}
			rt.soarPCs[zone] = pc
		}
		m.mu.Lock()
		if m.ctx.Err() != nil {
			m.mu.Unlock()
			rt.stop()
			return nil
		}
		m.accounts[c.ID] = rt
		for _, zone := range rt.zones {
			m.zones[zone] = rt
		}
		m.workers.Add(1)
		m.mu.Unlock()
		go m.runAccount(rt, seed)
	}
	return nil
}
func (m *Manager) Run() error {
	if e := m.Reconcile(); e != nil {
		return e
	}
	m.startCottenWorkers()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-m.ctx.Done():
			m.pc.Close()
		case <-done:
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				_ = m.Reconcile()
			}
		}
	}()
	defer func() {
		m.cancel()
		m.reconcileMu.Lock()
		m.mu.Lock()
		var retired []*accountRuntime
		for _, r := range m.accounts {
			r.retire()
			retired = append(retired, r)
		}
		m.mu.Unlock()
		for _, r := range retired {
			r.closeIO()
		}
		m.reconcileMu.Unlock()
		m.workers.Wait()
	}()
	b := make([]byte, 65535)
	for {
		n, a, e := m.pc.ReadFrom(b)
		if e != nil {
			if m.ctx.Err() != nil || errors.Is(e, net.ErrClosed) {
				return nil
			}
			return e
		}
		m.dnsIn.Add(uint64(n))
		m.routeDNS(b[:n], a, false, func(response []byte) {
			if len(response) > 0 {
				_, _ = m.writeDNSUDP(response, a)
			}
		})
	}
}

func dnsQuestion(b []byte) (name string, end int, typ uint16, ok bool) {
	if len(b) < 17 || b[2]&0xf8 != 0 || binary.BigEndian.Uint16(b[4:6]) != 1 {
		return
	}
	pos := 12
	seen := map[int]bool{}
	var labels []string
	total := 0
	for step := 0; step < 128; step++ {
		if pos >= len(b) || seen[pos] {
			return
		}
		seen[pos] = true
		n := int(b[pos])
		pos++
		if n&0xc0 == 0xc0 {
			if pos >= len(b) {
				return
			}
			if end == 0 {
				end = pos + 1
			}
			pos = ((n & 63) << 8) | int(b[pos])
			continue
		}
		if n&0xc0 != 0 || n > 63 || pos+n > len(b) {
			return
		}
		if n == 0 {
			if end == 0 {
				end = pos
			}
			if end+4 > len(b) || binary.BigEndian.Uint16(b[end+2:end+4]) != 1 {
				return
			}
			typ = binary.BigEndian.Uint16(b[end : end+2])
			end += 4
			name = strings.ToLower(strings.Join(labels, "."))
			ok = true
			return
		}
		total += n + 1
		if total > 254 {
			return
		}
		labels = append(labels, string(b[pos:pos+n]))
		pos += n
	}
	return
}
func dnsName(s string) []byte {
	var out []byte
	for _, p := range strings.Split(s, ".") {
		out = append(out, byte(len(p)))
		out = append(out, p...)
	}
	return append(out, 0)
}
func dnsControl(query []byte, end int, name string, typ uint16, base string, nameserver ...string) []byte {
	// Questions containing compression pointers are not reflected into small
	// control responses, because their referenced offsets may not be retained.
	if end > 512 || end < 17 {
		return nil
	}
	for i := 12; i < end-4; {
		n := int(query[i])
		if n&0xc0 != 0 {
			return nil
		}
		i += n + 1
		if n == 0 {
			break
		}
	}
	out := append([]byte{}, query[:end]...)
	flags := uint16(0x8405) | (binary.BigEndian.Uint16(query[2:4]) & 0x0100)
	binary.BigEndian.PutUint16(out[6:8], 0)
	binary.BigEndian.PutUint16(out[8:10], 0)
	binary.BigEndian.PutUint16(out[10:12], 0)
	ns := base
	if len(nameserver) > 0 && nameserver[0] != "" {
		ns = nameserver[0]
	}
	if name == base && base != "" && typ == 2 && ns != base {
		binary.BigEndian.PutUint16(out[2:4], flags&^15)
		binary.BigEndian.PutUint16(out[6:8], 1)
		out = append(out, 0xc0, 0x0c)
		out = binary.BigEndian.AppendUint16(out, 2)
		out = binary.BigEndian.AppendUint16(out, 1)
		out = binary.BigEndian.AppendUint32(out, 30)
		data := dnsName(ns)
		out = binary.BigEndian.AppendUint16(out, uint16(len(data)))
		return append(out, data...)
	}
	if (name == base || strings.HasSuffix(name, "."+base)) && (typ == 6 || typ == 2) {
		flags &^= 15
		data := append(dnsName(ns), dnsName("hostmaster."+base)...)
		for _, n := range []uint32{1, 300, 60, 3600, 30} {
			data = binary.BigEndian.AppendUint32(data, n)
		}
		if typ == 6 && name == base {
			binary.BigEndian.PutUint16(out[6:8], 1)
		} else {
			binary.BigEndian.PutUint16(out[8:10], 1)
		}
		if name == base {
			out = append(out, 0xc0, 0x0c)
		} else {
			out = append(out, dnsName(base)...)
		}
		out = binary.BigEndian.AppendUint16(out, 6)
		out = binary.BigEndian.AppendUint16(out, 1)
		out = binary.BigEndian.AppendUint32(out, 30)
		out = binary.BigEndian.AppendUint16(out, uint16(len(data)))
		out = append(out, data...)
	}
	binary.BigEndian.PutUint16(out[2:4], flags)
	return out
}

func (m *Manager) dial(r *accountRuntime, ctx context.Context, network, addr string) (net.Conn, error) {
	return m.dialEgress(r, ctx, network, addr, false)
}

func (m *Manager) dialForEngine(r *accountRuntime, ctx context.Context, network, addr string, engine upstreamEngine) (net.Conn, error) {
	return m.dialEgressFor(r, ctx, network, addr, false, engine)
}
func (m *Manager) dialEgress(r *accountRuntime, ctx context.Context, network, addr string, resolver bool) (net.Conn, error) {
	engine := upstreamUnknown
	if resolver {
		engine = upstreamCotten
	}
	return m.dialEgressFor(r, ctx, network, addr, resolver, engine)
}
func (m *Manager) dialEgressFor(r *accountRuntime, ctx context.Context, network, addr string, resolver bool, engine upstreamEngine) (net.Conn, error) {
	c, e := m.store.Get(r.client.ID)
	if e != nil || c.Status(time.Now()) != "active" {
		m.diagnostics.rejectedInactive.Add(1)
		return nil, ErrDenied
	}
	_, port, e := net.SplitHostPort(addr)
	if e != nil {
		return nil, e
	}
	p, e := strconv.ParseUint(port, 10, 16)
	if e != nil {
		return nil, e
	}
	m.mu.RLock()
	allowed := false
	for _, v := range m.cfg.AllowPorts {
		if uint64(v) == p {
			allowed = true
		}
	}
	m.mu.RUnlock()
	if resolver {
		allowed = false
		m.mu.RLock()
		for _, endpoint := range m.cfg.cottenResolvers() {
			if endpoint == addr {
				allowed = true
				break
			}
		}
		m.mu.RUnlock()
	}
	if !allowed {
		m.diagnostics.rejectedPolicy.Add(1)
		return nil, errDestinationPort
	}
	r.mu.Lock()
	if r.closed || c.StreamLimit > 0 && len(r.conns)+r.pending >= c.StreamLimit {
		r.mu.Unlock()
		m.diagnostics.rejectedAccountLimit.Add(1)
		return nil, errAccountStreamLimit
	}
	r.pending++
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.pending--; r.mu.Unlock() }()
	if !m.reserveStream() {
		m.diagnostics.rejectedServerLimit.Add(1)
		return nil, errServerStreamCapacity
	}
	transferred := false
	defer func() {
		if !transferred {
			m.active.Add(^uint64(0))
		}
	}()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(r.ctx, cancel)
	defer stop()
	dial := m.dialOverride
	if dial == nil {
		dial = (&net.Dialer{Timeout: 15 * time.Second, Control: soar.PublicOnly}).DialContext
	}
	m.diagnostics.dialAttempts.Add(1)
	started := time.Now()
	upstream, e := dial(ctx, network, addr)
	m.diagnostics.recordDial(time.Since(started), e != nil)
	if e != nil {
		return nil, e
	}
	mc := &meterConn{Conn: upstream, manager: m, rt: r, datagram: strings.HasPrefix(network, "udp"), engine: engine}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		upstream.Close()
		return nil, ErrDenied
	}
	r.conns[mc] = struct{}{}
	m.diagnostics.upstreamOpened(engine)
	transferred = true
	return mc, nil
}

// The settings read lock serializes admission with a global limit change.
// Pending dials reserve the same capacity as established streams and refund it
// on failure; closing a connection releases its reservation exactly once.
func (m *Manager) reserveStream() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for {
		active := m.active.Load()
		if m.cfg.ServerStreamLimit > 0 && active >= uint64(m.cfg.ServerStreamLimit) {
			return false
		}
		if m.active.CompareAndSwap(active, active+1) {
			return true
		}
	}
}
func (m *Manager) ActiveCounts() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v := map[string]int{}
	for id, r := range m.accounts {
		r.mu.Lock()
		v[id] = len(r.conns)
		r.mu.Unlock()
	}
	return v
}
func (m *Manager) Disconnect(id string) {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	m.mu.Lock()
	r := m.accounts[id]
	if r != nil {
		delete(m.accounts, id)
		for _, zone := range r.zones {
			delete(m.zones, zone)
		}
		r.retire()
	}
	m.mu.Unlock()
	if r != nil {
		r.closeIO()
	}
}

type meterConn struct {
	engine   upstreamEngine
	datagram bool
	net.Conn
	manager                     *Manager
	rt                          *accountRuntime
	once                        sync.Once
	stateMu                     sync.Mutex
	closed                      bool
	done                        chan struct{}
	deadlineChanged             chan struct{}
	readDeadline, writeDeadline time.Time
}

func (c *meterConn) Close() error {
	var e error
	c.once.Do(func() {
		c.stateMu.Lock()
		c.initIOStateLocked()
		c.closed = true
		close(c.done)
		c.stateMu.Unlock()
		e = c.Conn.Close()
		c.rt.mu.Lock()
		delete(c.rt.conns, c)
		c.rt.mu.Unlock()
		c.manager.active.Add(^uint64(0))
		c.manager.diagnostics.upstreamClosed(c.engine, e)
	})
	return e
}
func (c *meterConn) CloseWrite() error {
	if v, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return v.CloseWrite()
	}
	return nil
}
func (c *meterConn) chunk(p []byte, rate int64) []byte {
	n := min(len(p), 32*1024)
	if rate > 0 {
		n = min(n, max(1, int(rate/4)))
	}
	return p[:n]
}
func (c *meterConn) Read(p []byte) (int, error) {
	if c.datagram {
		return c.readDatagram(p)
	}
	info, e := c.manager.store.Get(c.rt.client.ID)
	if e != nil || info.Status(time.Now()) != "active" {
		return 0, ErrDenied
	}
	n, e := c.Conn.Read(c.chunk(p, info.RateBytes))
	if n == 0 {
		return 0, e
	}
	if err := c.pace(n, info.RateBytes, true); err != nil {
		return 0, err
	}
	// Bill payload after it exists, before it can be forwarded to the client.
	if c.rt.ctx.Err() != nil {
		return 0, ErrDenied
	}
	allowed, _, err := c.manager.store.reserveEpoch(info.ID, n, false, c.rt.client.UsageEpoch)
	if err != nil {
		return 0, err
	}
	if allowed < n {
		return allowed, ErrDenied
	}
	return n, e
}
func (c *meterConn) Write(p []byte) (int, error) {
	if c.datagram {
		return c.writeDatagram(p)
	}
	total := 0
	for len(p) > 0 {
		info, e := c.manager.store.Get(c.rt.client.ID)
		if e != nil || info.Status(time.Now()) != "active" {
			return total, ErrDenied
		}
		part := c.chunk(p, info.RateBytes)
		if e := c.pace(len(part), info.RateBytes, false); e != nil {
			return total, e
		}
		if c.rt.ctx.Err() != nil {
			return total, ErrDenied
		}
		allowed, epoch, e := c.manager.store.reserveEpoch(info.ID, len(part), true, c.rt.client.UsageEpoch)
		if e != nil {
			return total, e
		}
		n, e := c.Conn.Write(part[:allowed])
		if refundErr := c.manager.store.refund(info.ID, allowed-n, true, epoch); refundErr != nil {
			c.Close()
			return total + n, refundErr
		}
		total += n
		p = p[n:]
		if e != nil {
			return total, e
		}
		if n < allowed {
			return total, io.ErrShortWrite
		}
		if allowed < len(part) {
			return total, ErrDenied
		}
	}
	return total, nil
}
