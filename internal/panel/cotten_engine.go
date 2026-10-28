package panel

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	cotten "cottendns-go/server"
	"github.com/getlantern/soar"
)

type cottenJob struct {
	master bool
	rt     *accountRuntime
	query  []byte
	reply  func([]byte)
	tcp    bool
}

func (m *Manager) runAccount(rt *accountRuntime, seed []byte) {
	defer m.workers.Done()
	defer rt.stop()
	soarDone := make(chan struct{})
	if rt.client.hasEngine("soar") {
		var servers sync.WaitGroup
		for zone, pc := range rt.soarPCs {
			servers.Add(1)
			go func() {
				defer servers.Done()
				defer rt.cancel()
				totalStreams, sessionStreams := soarStreamLimits(rt.client.StreamLimit)
				m.diagnostics.soarStarts.Add(1)
				m.diagnostics.soarRunning.Add(1)
				m.logEvent("info", "soar", rt.client.ID, "engine_started", nil, 0)
				err := soar.Serve(rt.ctx, pc, soar.ServerConfig{Zone: zone, PrivateKey: ed25519.NewKeyFromSeed(seed), Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					return m.dialForEngine(rt, ctx, network, address, upstreamSoar)
				}, MaxSessions: rt.client.SoarSessionLimit, MaxTotalStreams: totalStreams, MaxStreams: sessionStreams, Logger: slog.New(engineDiagnosticHandler{&m.diagnostics})})
				m.diagnostics.soarRunning.Add(-1)
				m.diagnostics.soarExits.Add(1)
				if rt.ctx.Err() == nil {
					m.logEvent("error", "soar", rt.client.ID, "engine_unexpected_exit", err, 0)
					m.diagnostics.soarUnexpectedExits.Add(1)
					m.diagnostics.lastEngineIssueUnixMS.Store(time.Now().UnixMilli())
					if err != nil {
						m.diagnostics.soarServeErrors.Add(1)
					}
				}
				m.logEvent("info", "soar", rt.client.ID, "engine_stopped", nil, 0)
			}()
		}
		go func() { servers.Wait(); close(soarDone) }()
	} else {
		close(soarDone)
	}
	<-rt.ctx.Done()
	rt.stop()
	rt.cottenMu.Lock()
	engine := rt.cotten
	rt.cottenMu.Unlock()
	if engine != nil {
		if err := engine.Close(); err != nil {
			m.logEvent("error", "cotten", rt.client.ID, "engine_close_failed", err, 0)
			m.diagnostics.cottenCloseErrors.Add(1)
			m.diagnostics.lastEngineIssueUnixMS.Store(time.Now().UnixMilli())
		}
		m.diagnostics.cottenRunning.Add(-1)
		m.logEvent("info", "cotten", rt.client.ID, "engine_stopped", nil, 0)
	}
	rt.masterMu.Lock()
	master := rt.master
	rt.masterMu.Unlock()
	if master != nil {
		if err := master.Close(); err != nil {
			m.logEvent("error", "masterdns", rt.client.ID, "engine_close_failed", err, 0)
			m.diagnostics.masterCloseErrors.Add(1)
		}
		m.diagnostics.masterRunning.Add(-1)
		m.logEvent("info", "masterdns", rt.client.ID, "engine_stopped", nil, 0)
	}
	<-soarDone
}

func (m *Manager) cottenFor(rt *accountRuntime) (*cotten.Server, error) {
	rt.cottenMu.Lock()
	defer rt.cottenMu.Unlock()
	if rt.ctx.Err() != nil {
		return nil, ErrDenied
	}
	if rt.cotten != nil {
		return rt.cotten, nil
	}
	seed, err := m.store.Seed(rt.client.ID)
	if err != nil {
		return nil, err
	}
	domains := make([]string, len(rt.zones))
	for i, zone := range rt.zones {
		domains[i] = "cot." + zone
	}
	engine, err := cotten.New(rt.ctx, cotten.Config{Domain: domains[0], Domains: domains, Key: cottenKey(seed), MaxActiveSessions: rt.client.CottenSessionLimit, MaxStreamsPerSession: cottenStreamLimit(rt.client.StreamLimit),
		Allowed: func() bool {
			c, err := m.store.Get(rt.client.ID)
			return err == nil && c.Status(time.Now()) == "active" && c.hasEngine("cotten") && c.UsageEpoch == rt.client.UsageEpoch && c.PublicKey == rt.client.PublicKey
		},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return m.dialForEngine(rt, ctx, network, address, upstreamCotten)
		},
		ExchangeDNS: func(ctx context.Context, query []byte) ([]byte, error) { return m.cottenDNS(rt, ctx, query) },
	})
	if err != nil {
		m.diagnostics.cottenStartErrors.Add(1)
		m.logEvent("error", "cotten", rt.client.ID, "engine_start_failed", err, 0)
		m.diagnostics.lastEngineIssueUnixMS.Store(time.Now().UnixMilli())
		return nil, err
	}
	m.diagnostics.cottenStarts.Add(1)
	m.diagnostics.cottenRunning.Add(1)
	m.logEvent("info", "cotten", rt.client.ID, "engine_started", nil, 0)
	rt.cotten = engine
	return engine, nil
}

func soarStreamLimits(accountLimit int) (total, perSession int) {
	if accountLimit == 0 {
		return MaxStreamLimit, 256
	}
	return accountLimit, min(accountLimit, 256)
}

func cottenStreamLimit(accountLimit int) int {
	if accountLimit == 0 {
		return 65535
	}
	return min(accountLimit, 65535)
}

func (m *Manager) startCottenWorkers() {
	for range min(max(runtime.GOMAXPROCS(0), 2), 8) {
		m.workers.Add(1)
		go func() {
			defer m.workers.Done()
			for {
				select {
				case <-m.ctx.Done():
					return
				case job := <-m.cottenJobs:
					var response []byte
					if job.master {
						if engine, err := m.masterDNSFor(job.rt); err == nil {
							if job.tcp {
								response = engine.HandleDNSTCP(job.query)
							} else {
								response = engine.HandleDNS(job.query)
							}
						}
					} else if engine, err := m.cottenFor(job.rt); err == nil {
						if job.tcp {
							response = engine.HandleDNSTCP(job.query)
						} else {
							response = engine.HandleDNS(job.query)
						}
					}
					job.reply(response)
				}
			}
		}()
	}
}

// All engines live beneath the existing delegation. The internal "cot" and
// "mdns-1" labels keep account keys isolated without extra operator DNS records.
func (m *Manager) routeDNS(query []byte, remote net.Addr, tcp bool, reply func([]byte)) {
	name, end, typ, ok := dnsQuestion(query)
	if !ok {
		reply(nil)
		return
	}
	m.mu.RLock()
	base := m.cfg.matchTunnelZone(name)
	nameserver := ""
	if base != "" {
		nameserver = m.cfg.nameserverFor(base)
	}
	if response := m.domainProbeReply(query, name, typ, base); response != nil {
		m.mu.RUnlock()
		reply(response)
		return
	}
	// Recursive resolvers may minimise QNAMEs at either account or engine
	// labels. Answer authoritative NS/SOA control queries before tunnel routing.
	if typ == 2 || typ == 6 {
		m.mu.RUnlock()
		reply(dnsControl(query, end, name, typ, base, nameserver))
		return
	}
	var rt *accountRuntime
	zone := ""
	if strings.HasSuffix(name, "."+base) {
		parts := strings.Split(strings.TrimSuffix(name, "."+base), ".")
		zone = parts[len(parts)-1] + "." + base
		rt = m.zones[zone]
	}
	m.mu.RUnlock()
	if rt != nil {
		cottenZone := "cot." + zone
		cottenQuery := name == cottenZone || strings.HasSuffix(name, "."+cottenZone)
		masterZone := "mdns-1." + zone
		masterQuery := name == masterZone || strings.HasSuffix(name, "."+masterZone)
		if masterQuery && rt.client.hasEngine("masterdns") {
			job := cottenJob{rt: rt, query: append([]byte(nil), query...), reply: reply, tcp: tcp, master: true}
			select {
			case m.cottenJobs <- job:
				return
			default:
				m.diagnostics.masterQueueDrops.Add(1)
				m.logEvent("warn", "masterdns", rt.client.ID, "dns_queue_full", nil, 0)
			}
		} else if cottenQuery && rt.client.hasEngine("cotten") {
			job := cottenJob{rt: rt, query: append([]byte(nil), query...), reply: reply, tcp: tcp}
			select {
			case m.cottenJobs <- job:
				return
			default:
				m.diagnostics.cottenQueueDrops.Add(1)
				m.logEvent("warn", "cotten", rt.client.ID, "dns_queue_full", nil, 0)
			}
		} else if !cottenQuery && !masterQuery && !tcp && rt.client.hasEngine("soar") {
			q := packet{append([]byte(nil), query...), remote}
			select {
			case rt.soarPCs[zone].input <- q:
			case <-rt.soarPCs[zone].done:
			default:
				m.diagnostics.soarQueueDrops.Add(1)
				m.logEvent("warn", "soar", rt.client.ID, "dns_queue_full", nil, 0)
			}
			return
		}
	}
	reply(dnsControl(query, end, name, typ, base, nameserver))
}

// ServeTCP owns bounded DNS framing, not a second engine listener. A connection
// may contain questions for different accounts, so routing is per message.
func (m *Manager) ServeTCP(listener net.Listener) error {
	var mu sync.Mutex
	open := make(map[net.Conn]struct{})
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		select {
		case <-m.ctx.Done():
		case <-done:
		}
		listener.Close()
		mu.Lock()
		for c := range open {
			c.Close()
		}
		mu.Unlock()
	}()
	defer func() { close(done); wg.Wait() }()
	var acceptDelay time.Duration
	for {
		c, err := listener.Accept()
		if err != nil {
			if m.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			if recoverableAcceptError(err) {
				m.diagnostics.tcpAcceptRetries.Add(1)
				m.logEvent("warn", "connection", "", "dns_tcp_accept_retry", err, 0)
				if acceptDelay == 0 {
					acceptDelay = 5 * time.Millisecond
				} else {
					acceptDelay = min(acceptDelay*2, time.Second)
				}
				timer := time.NewTimer(acceptDelay)
				select {
				case <-m.ctx.Done():
					timer.Stop()
					return nil
				case <-timer.C:
				}
				continue
			}
			return err
		}
		acceptDelay = 0
		mu.Lock()
		if m.ctx.Err() != nil || len(open) >= 128 {
			mu.Unlock()
			c.Close()
			continue
		}
		open[c] = struct{}{}
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { c.Close(); mu.Lock(); delete(open, c); mu.Unlock() }()
			for {
				c.SetReadDeadline(time.Now().Add(30 * time.Second))
				var size [2]byte
				if _, err := io.ReadFull(c, size[:]); err != nil {
					return
				}
				n := int(binary.BigEndian.Uint16(size[:]))
				if n < 17 {
					return
				}
				query := make([]byte, n)
				if _, err := io.ReadFull(c, query); err != nil {
					return
				}
				m.dnsIn.Add(uint64(n))
				responses := make(chan []byte, 1)
				m.routeDNS(query, c.RemoteAddr(), true, func(p []byte) { responses <- p })
				var response []byte
				select {
				case response = <-responses:
				case <-m.ctx.Done():
					return
				}
				if len(response) == 0 {
					return
				}
				frame := binary.BigEndian.AppendUint16(nil, uint16(len(response)))
				frame = append(frame, response...)
				c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := io.Copy(c, strings.NewReader(string(frame))); err != nil {
					return
				}
				m.dnsOut.Add(uint64(len(response)))
			}
		}()
	}
}

func recoverableAcceptError(err error) bool {
	if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) {
		return true
	}
	var temporary interface{ Temporary() bool }
	return errors.As(err, &temporary) && temporary.Temporary()
}

func (m *Manager) cottenDNS(rt *accountRuntime, parent context.Context, query []byte) ([]byte, error) {
	return m.tunnelDNS(rt, parent, query, upstreamCotten)
}
func (m *Manager) tunnelDNS(rt *accountRuntime, parent context.Context, query []byte, engine upstreamEngine) ([]byte, error) {
	if _, _, _, ok := dnsQuestion(query); !ok || len(query) > 4096 {
		return nil, errors.New("invalid tunneled DNS query")
	}
	m.mu.RLock()
	resolvers := m.cfg.cottenResolvers()
	route := m.egress.route(engine)
	m.mu.RUnlock()
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()
	candidates := rt.orderedResolvers(resolvers, time.Now())
	for i, candidate := range candidates {
		endpoint := candidate.endpoint
		deadline, _ := ctx.Deadline()
		budget := min(2*time.Second, time.Until(deadline)/time.Duration(len(candidates)-i))
		if candidate.probe {
			budget = min(budget, 250*time.Millisecond)
		}
		if budget <= 0 {
			break
		}
		attempt, stop := context.WithTimeout(ctx, budget)
		started := time.Now()
		network := "udp"
		if route.Mode == "http" || route.Mode == "https" {
			network = "tcp"
		}
		response, err := m.exchangeDNSFor(rt, attempt, network, endpoint, query, engine)
		if err != nil {
			stop()
			if errors.Is(err, errAntiFlood) || errors.Is(err, ErrDenied) {
				return nil, err
			}
			if parent.Err() == nil && resolverFailure(err, rt) {
				rt.resolverResult(endpoint, time.Since(started), false)
				m.logEvent("warn", engineLogSource(engine), rt.client.ID, "resolver_query_failed", err, time.Since(started))
			}
			continue
		}
		if len(response) >= 4 && response[2]&2 != 0 {
			response, err = m.exchangeDNSFor(rt, attempt, "tcp", endpoint, query, engine)
		}
		stop()
		if err == nil && len(response) >= 12 && response[0] == query[0] && response[1] == query[1] && response[2]&0x80 != 0 {
			if code := response[3] & 15; code == 2 || code == 5 {
				m.logEvent("warn", engineLogSource(engine), rt.client.ID, "resolver_refused_or_servfail", nil, time.Since(started))
				rt.resolverResult(endpoint, time.Since(started), false)
				continue
			}
			rt.resolverResult(endpoint, time.Since(started), true)
			return response, nil
		}
		if parent.Err() == nil && resolverFailure(err, rt) {
			rt.resolverResult(endpoint, time.Since(started), false)
			m.logEvent("warn", engineLogSource(engine), rt.client.ID, "resolver_invalid_response", err, time.Since(started))
		}
	}
	return nil, errors.New("DNS resolver unavailable")
}

func resolverFailure(err error, rt *accountRuntime) bool {
	return rt.ctx.Err() == nil && !errors.Is(err, ErrDenied) && !errors.Is(err, context.Canceled) && !errors.Is(err, errPacingDeadline) && !errors.Is(err, errAccountStreamLimit) && !errors.Is(err, errServerStreamCapacity) && !errors.Is(err, errDestinationPort) && !errors.Is(err, errAntiFlood)
}

func resolverEndpoint(resolver string) string {
	if address, err := netip.ParseAddr(resolver); err == nil {
		return net.JoinHostPort(address.String(), "53")
	}
	if address, err := netip.ParseAddrPort(resolver); err == nil {
		return address.String()
	}
	return ""
}

// Bootstrap resolvers may belong to a phone carrier and be unreachable from a
// VPS. Keep public server-side fallbacks without exposing another install prompt.
func (s Settings) cottenResolvers() []string {
	var result []string
	seen := map[string]bool{}
	for _, resolver := range append(append([]string(nil), s.Resolvers...), "1.1.1.1", "8.8.8.8") {
		endpoint := resolverEndpoint(resolver)
		if endpoint != "" && !seen[endpoint] {
			seen[endpoint] = true
			result = append(result, endpoint)
		}
	}
	return result
}

func (m *Manager) exchangeCottenDNS(rt *accountRuntime, ctx context.Context, network, address string, query []byte) ([]byte, error) {
	return m.exchangeDNSFor(rt, ctx, network, address, query, upstreamCotten)
}
func (m *Manager) exchangeDNSFor(rt *accountRuntime, ctx context.Context, network, address string, query []byte, engine upstreamEngine) ([]byte, error) {
	c, err := m.dialEgressFor(rt, ctx, network, address, true, engine)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		c.SetDeadline(deadline)
	}
	if network == "udp" {
		if _, err = c.Write(query); err != nil {
			return nil, err
		}
		buf := make([]byte, 65535)
		n, err := c.Read(buf)
		return buf[:n], err
	}
	frame := binary.BigEndian.AppendUint16(nil, uint16(len(query)))
	frame = append(frame, query...)
	if _, err = c.Write(frame); err != nil {
		return nil, err
	}
	var size [2]byte
	if _, err = io.ReadFull(c, size[:]); err != nil {
		return nil, err
	}
	buf := make([]byte, int(binary.BigEndian.Uint16(size[:])))
	_, err = io.ReadFull(c, buf)
	return buf, err
}
