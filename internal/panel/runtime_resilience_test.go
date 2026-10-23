package panel

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type resiliencePacketConn struct{ writeErr error }

func (p *resiliencePacketConn) ReadFrom([]byte) (int, net.Addr, error) { return 0, nil, net.ErrClosed }
func (p *resiliencePacketConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	if p.writeErr != nil {
		return 0, p.writeErr
	}
	return len(b), nil
}
func (*resiliencePacketConn) Close() error                     { return nil }
func (*resiliencePacketConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (*resiliencePacketConn) SetDeadline(time.Time) error      { return nil }
func (*resiliencePacketConn) SetReadDeadline(time.Time) error  { return nil }
func (*resiliencePacketConn) SetWriteDeadline(time.Time) error { return nil }

func resilienceManager(t *testing.T, rate int64) (*Manager, *accountRuntime, Client) {
	t.Helper()
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "Runtime regression", Enabled: true, Engines: "both", RateBytes: rate, StreamLimit: 0})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(context.Background(), s, &resiliencePacketConn{})
	m.cfg, err = s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(m.ctx)
	rt := &accountRuntime{client: c, ctx: ctx, cancel: cancel, pc: &virtualPacketConn{done: make(chan struct{})}, conns: map[*meterConn]struct{}{}, zones: m.cfg.accountZones(c), soarPCs: map[string]*virtualPacketConn{}}
	m.accounts[c.ID] = rt
	for _, zone := range rt.zones {
		m.zones[zone] = rt
	}
	m.dialOverride = func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() { defer b.Close(); _, _ = io.Copy(io.Discard, b) }()
		return a, nil
	}
	t.Cleanup(func() { m.cancel(); rt.stop() })
	return m, rt, c
}

func TestPacingCloseCancelsQueueWithoutFutureDebt(t *testing.T) {
	m, rt, _ := resilienceManager(t, 4096)
	var conns []net.Conn
	done := make(chan error, 20)
	for range 20 {
		c, err := m.dial(rt, context.Background(), "tcp", "example.org:443")
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		go func() { _, err := c.Write(make([]byte, 1024)); done <- err }()
	}
	limit := time.Now().Add(time.Second)
	for {
		rt.rateMu.Lock()
		queued := 0
		for p := rt.rateHead; p != nil; p = p.next {
			queued++
		}
		rt.rateMu.Unlock()
		if queued == 20 {
			break
		}
		if time.Now().After(limit) {
			t.Fatalf("expected queued writes, got %d", queued)
		}
		time.Sleep(time.Millisecond)
	}
	for _, c := range conns {
		_ = c.Close()
	}
	for range conns {
		select {
		case err := <-done:
			if !errors.Is(err, net.ErrClosed) {
				t.Fatalf("closed write returned %v", err)
			}
		case <-time.After(250 * time.Millisecond):
			t.Fatal("closed stream remained in bandwidth wait")
		}
	}
	rt.rateMu.Lock()
	empty := rt.rateHead == nil && rt.rateTail == nil
	rt.rateMu.Unlock()
	if !empty {
		t.Fatal("cancelled writes left account pacing debt")
	}
	c, err := m.dial(rt, context.Background(), "tcp", "example.org:443")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	if _, err = c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("new connection waited on closed streams' pacing debt")
	}
}

func TestPacingHonorsChangedReadAndWriteDeadlines(t *testing.T) {
	for _, read := range []bool{false, true} {
		t.Run(map[bool]string{false: "write", true: "read"}[read], func(t *testing.T) {
			m, rt, client := resilienceManager(t, 1024)
			peerDone := make(chan struct{})
			peerRelease := make(chan struct{})
			var releasePeer sync.Once
			defer releasePeer.Do(func() { close(peerRelease) })
			if read {
				m.dialOverride = func(context.Context, string, string) (net.Conn, error) {
					a, b := net.Pipe()
					go func() {
						defer close(peerDone)
						defer b.Close()
						_, _ = b.Write(make([]byte, 256))
						<-peerRelease
					}()
					return a, nil
				}
			}
			c, err := m.dial(rt, context.Background(), "tcp", "example.org:443")
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			done := make(chan error, 1)
			go func() {
				if read {
					_, err := c.Read(make([]byte, 256))
					done <- err
				} else {
					_, err := c.Write(make([]byte, 256))
					done <- err
				}
			}()
			limit := time.Now().Add(time.Second)
			for {
				rt.rateMu.Lock()
				queued := rt.rateHead != nil
				rt.rateMu.Unlock()
				if queued {
					break
				}
				if time.Now().After(limit) {
					t.Fatal("operation did not enter pacing")
				}
				time.Sleep(time.Millisecond)
			}
			if read {
				err = c.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
			} else {
				err = c.SetWriteDeadline(time.Now().Add(20 * time.Millisecond))
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
				if !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("deadline returned %v", err)
				}
			case <-time.After(150 * time.Millisecond):
				t.Fatal("pacing ignored changed operation deadline")
			}
			actual, err := m.store.Get(client.ID)
			if err != nil || actual.Used() != 0 {
				t.Fatalf("deadline-expired payload was billed: used=%d err=%v", actual.Used(), err)
			}
			if read {
				_ = c.Close()
				releasePeer.Do(func() { close(peerRelease) })
				<-peerDone
			}
		})
	}
}

type resilienceSlowClose struct {
	net.Conn
	entered, release chan struct{}
	once             sync.Once
}

func (c *resilienceSlowClose) Close() error {
	c.once.Do(func() { close(c.entered); <-c.release })
	return nil
}
func TestReconcileRetirementDoesNotBlockOtherDNS(t *testing.T) {
	m, rt, c := resilienceManager(t, 0)
	slow := &resilienceSlowClose{entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(slow.release) })
	mc := &meterConn{Conn: slow, manager: m, rt: rt}
	rt.conns[mc] = struct{}{}
	m.active.Add(1)
	in := input(c)
	in.Enabled = false
	if _, err := m.store.Edit(c.ID, in); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.Reconcile() }()
	select {
	case <-slow.entered:
	case <-time.After(time.Second):
		t.Fatal("retired socket not closed")
	}
	query := make([]byte, 12)
	binary.BigEndian.PutUint16(query[4:6], 1)
	query = append(query, dnsName(m.cfg.BaseZone)...)
	query = binary.BigEndian.AppendUint16(query, 6)
	query = binary.BigEndian.AppendUint16(query, 1)
	answered := make(chan []byte, 1)
	go m.routeDNS(query, nil, false, func(response []byte) { answered <- response })
	select {
	case response := <-answered:
		if len(response) < 12 {
			t.Fatal("invalid authority response")
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("retired account Close blocked unrelated DNS routing")
	}
	release.Do(func() { close(slow.release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type resilienceListener struct {
	calls      atomic.Int32
	alwaysFail bool
	closed     chan struct{}
	once       sync.Once
}

func (l *resilienceListener) Accept() (net.Conn, error) {
	n := l.calls.Add(1)
	if n == 1 || l.alwaysFail {
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: syscall.EMFILE}
	}
	<-l.closed
	return nil, net.ErrClosed
}
func (l *resilienceListener) Close() error { l.once.Do(func() { close(l.closed) }); return nil }
func (*resilienceListener) Addr() net.Addr { return &net.TCPAddr{} }
func TestTCPAcceptRetriesDescriptorPressureAndCancelsBackoff(t *testing.T) {
	for _, always := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovers", true: "cancel"}[always], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := NewManager(ctx, nil, &resiliencePacketConn{})
			l := &resilienceListener{alwaysFail: always, closed: make(chan struct{})}
			done := make(chan error, 1)
			go func() { done <- m.ServeTCP(l) }()
			limit := time.Now().Add(time.Second)
			for l.calls.Load() < 2 {
				select {
				case err := <-done:
					t.Fatalf("recoverable accept error terminated DNS service: %v", err)
				default:
				}
				if time.Now().After(limit) {
					t.Fatal("DNS accept did not retry")
				}
				time.Sleep(time.Millisecond)
			}
			if m.Diagnostics().TCPAcceptRetries == 0 {
				t.Fatal("accept pressure not observable")
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(150 * time.Millisecond):
				t.Fatal("TCP accept backoff ignored shutdown")
			}
		})
	}
}

func TestRuntimeDiagnosticsRemainAggregateAndCountFailures(t *testing.T) {
	m, rt, _ := resilienceManager(t, 0)
	m.pc = &resiliencePacketConn{writeErr: errors.New("write failed")}
	_, _ = m.writeDNSUDP([]byte("x"), &net.UDPAddr{})
	logger := slog.New(engineDiagnosticHandler{&m.diagnostics})
	logger.Warn("private query secret.example", "key", "private-key")
	logger.Error("peer 198.51.100.7")
	c, err := m.dialForEngine(rt, context.Background(), "tcp", "example.org:443", upstreamCotten)
	if err != nil {
		t.Fatal(err)
	}
	if m.Diagnostics().CottenActiveStreams != 1 {
		t.Fatal("missing engine stream count")
	}
	_ = c.Close()
	m.dialOverride = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("private destination") }
	_, _ = m.dial(rt, context.Background(), "tcp", "example.org:443")
	d := m.Diagnostics()
	if d.UDPWriteErrors != 1 || d.SoarWarnings != 1 || d.SoarLogErrors != 1 || d.DialAttempts != 2 || d.DialFailures != 1 || d.CottenActiveStreams != 0 || d.CottenOpenedStreams != 1 || d.CottenClosedStreams != 1 {
		t.Fatalf("unexpected aggregate diagnostics: %+v", d)
	}
	b, _ := json.Marshal(d)
	for _, secret := range []string{"secret.example", "private-key", "198.51.100.7", "private destination"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("diagnostics retained traffic metadata")
		}
	}
}

func TestResolverHealthPrefersWorkingEndpointAndBoundsRecoveryProbe(t *testing.T) {
	r := &accountRuntime{}
	endpoints := []string{"1.1.1.1:53", "8.8.8.8:53"}
	r.resolverResult(endpoints[0], time.Second, false)
	r.resolverResult(endpoints[1], 20*time.Millisecond, true)
	ordered := r.orderedResolvers(endpoints, time.Now())
	if ordered[0].endpoint != endpoints[1] {
		t.Fatal("known failed bootstrap still comes before working resolver")
	}
	r.resolverMu.Lock()
	r.resolverHealth[endpoints[0]].retryAt = time.Now().Add(-time.Second)
	r.resolverMu.Unlock()
	ordered = r.orderedResolvers(endpoints, time.Now())
	if ordered[0].endpoint != endpoints[0] || !ordered[0].probe {
		t.Fatal("failed resolver did not receive bounded recovery probe")
	}
	if next := r.orderedResolvers(endpoints, time.Now()); next[0].endpoint != endpoints[1] {
		t.Fatal("parallel requests all received recovery probe")
	}
	if resolverFailure(errPacingDeadline, rWithContext()) || resolverFailure(errServerStreamCapacity, rWithContext()) {
		t.Fatal("local pacing/admission pressure penalized remote resolver")
	}
	if !resolverFailure(net.ErrClosed, rWithContext()) {
		t.Fatal("attempt-timeout socket closure was not recorded as resolver failure")
	}
}
func rWithContext() *accountRuntime { return &accountRuntime{ctx: context.Background()} }

func TestRuntimeDiagnosticsCountIngressDropsAndAdmissionRejections(t *testing.T) {
	m, rt, client := resilienceManager(t, 0)
	query := func(name string) []byte {
		b := make([]byte, 12)
		binary.BigEndian.PutUint16(b[4:6], 1)
		b = append(b, dnsName(name)...)
		b = binary.BigEndian.AppendUint16(b, 16)
		return binary.BigEndian.AppendUint16(b, 1)
	}
	rt.soarPCs[client.Zone] = &virtualPacketConn{owner: m, input: make(chan packet, 1), done: make(chan struct{})}
	m.routeDNS(query("x."+client.Zone), nil, false, func([]byte) {})
	m.routeDNS(query("x."+client.Zone), nil, false, func([]byte) {})
	m.cottenJobs = make(chan cottenJob, 1)
	m.routeDNS(query("x.cot."+client.Zone), nil, false, func([]byte) {})
	m.routeDNS(query("x.cot."+client.Zone), nil, true, func([]byte) {})
	if d := m.Diagnostics(); d.SoarQueueDrops != 1 || d.CottenQueueDrops != 1 {
		t.Fatalf("queue drops not counted: %+v", d)
	}
	in := input(client)
	in.StreamLimit = 1
	updated, err := m.store.Edit(client.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.dial(rt, context.Background(), "tcp", "example.org:443")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.dial(rt, context.Background(), "tcp", "example.org:443"); !errors.Is(err, errAccountStreamLimit) {
		t.Fatal("account cap not enforced")
	}
	_ = c.Close()
	in = input(updated)
	in.StreamLimit = 0
	if _, err = m.store.Edit(client.ID, in); err != nil {
		t.Fatal(err)
	}
	m.cfg.ServerStreamLimit = 1
	c, err = m.dial(rt, context.Background(), "tcp", "example.org:443")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.dial(rt, context.Background(), "tcp", "example.org:443"); !errors.Is(err, errServerStreamCapacity) {
		t.Fatal("server cap not enforced")
	}
	_ = c.Close()
	_, _ = m.dial(rt, context.Background(), "tcp", "example.org:22")
	d := m.Diagnostics()
	if d.RejectedAccountLimit != 1 || d.RejectedServerLimit != 1 || d.RejectedPolicy != 1 {
		t.Fatalf("admission diagnostics missing: %+v", d)
	}
}
