package panel

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestAntiFloodDestinationBudgetsArePerAccountUnderSharedCeiling(t *testing.T) {
	for _, kind := range []string{"target", "network"} {
		t.Run(kind, func(t *testing.T) {
			p := DefaultAntiFloodPolicy()
			p.TargetBurst, p.TargetRate, p.NetworkBurst, p.NetworkRate = 1, 1, 1, 1
			if kind == "target" {
				p.NetworkBurst, p.NetworkRate = 1000, 1000
			} else {
				p.TargetBurst, p.TargetRate = 1000, 1000
			}
			g, _, _, now := floodFixture(t, p)
			dial := func(id string, host byte) error {
				l, err, _ := g.begin(id, false, now)
				if err != nil {
					t.Fatal(err)
				}
				defer l.release()
				ip := netip.AddrFrom4([4]byte{192, 0, 2, 1})
				if kind == "network" {
					ip = netip.AddrFrom4([4]byte{192, 0, 2, host})
				}
				err, _ = g.target(l, ip, now)
				return err
			}
			if err := dial("account-0", 1); err != nil {
				t.Fatal(err)
			}
			if err := dial("account-0", 100); !errors.Is(err, errAntiFlood) {
				t.Fatal("per-account destination budget bypassed", err)
			}
			for i := 1; i < floodSharedFactor; i++ {
				if err := dial(fmt.Sprintf("account-%d", i), byte(i+1)); err != nil {
					t.Fatalf("account %d lost its own budget to other accounts: %v", i, err)
				}
			}
			if err := dial("one-more", 101); !errors.Is(err, errAntiFlood) {
				t.Fatal("shared destination ceiling bypassed", err)
			}
		})
	}
}

func TestAntiFloodConcurrentTargetStreamsArePerAccount(t *testing.T) {
	p := DefaultAntiFloodPolicy()
	p.TargetStreams = 2
	g, _, _, now := floodFixture(t, p)
	dc := netip.MustParseAddr("149.154.167.50")
	var open []*floodLease
	defer func() {
		for _, l := range open {
			l.release()
		}
	}()
	for _, id := range []string{"a", "a", "b", "b"} {
		l, err, _ := g.begin(id, false, now)
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, l)
		if err, _ := g.target(l, dc, now); err != nil {
			t.Fatal("other account consumed this account's streams", id, err)
		}
	}
	l, err, _ := g.begin("a", false, now)
	if err != nil {
		t.Fatal(err)
	}
	defer l.release()
	if err, _ := g.target(l, dc, now); !errors.Is(err, errAntiFlood) {
		t.Fatal("per-account target streams bypassed", err)
	}
	open[0].release()
	if err, _ := g.target(l, dc, now); err != nil {
		t.Fatal("released stream slot was not returned", err)
	}
}

func TestAntiFloodOutageWithoutHealthyDialsDoesNotQuarantine(t *testing.T) {
	p := DefaultAntiFloodPolicy()
	p.FailureThreshold = 10
	g, _, c, now := floodFixture(t, p)
	for i := range 30 {
		l, err, _ := g.begin(c.ID, false, now)
		if err != nil {
			t.Fatal(err)
		}
		if err, _ := g.target(l, netip.AddrFrom4([4]byte{192, 0, 2, byte(i + 1)}), now); err != nil {
			t.Fatal(err)
		}
		q := g.outcome(l, true, now)
		l.release()
		if q || g.blocked(c.ID) {
			t.Fatal("server-wide outage quarantined account", i)
		}
	}
}

func TestDeadEgressProxyDoesNotQuarantineAccount(t *testing.T) {
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "a", Engines: "soar", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	ln.Close()
	cfg, _ := s.Egress()
	cfg.Soar = EngineEgress{Mode: "socks5", Address: dead, Ports: "80,443"}
	if err = s.SaveEgress(cfg); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, s, nil)
	rt := m.accounts[c.ID]
	// Another route works, so only the proxy exclusion can prevent the quarantine.
	healthy, err, _ := m.flood.begin("healthy-account", false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m.flood.outcome(healthy, false, time.Now())
	healthy.release()
	var wg sync.WaitGroup
	for i := 1; i <= 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if conn, e := m.dialForEngine(rt, ctx, "tcp", fmt.Sprintf("8.8.%d.1:443", i), upstreamSoar); e == nil {
				conn.Close()
			}
		}()
	}
	wg.Wait()
	if m.flood.blocked(c.ID) {
		t.Fatal("dead proxy quarantined account")
	}
}

func regressionQuery(name string) []byte {
	b := make([]byte, 12)
	b[0], b[1] = 0x12, 0x34
	binary.BigEndian.PutUint16(b[4:6], 1)
	b = append(b, dnsName(name)...)
	b = binary.BigEndian.AppendUint16(b, 16)
	return binary.BigEndian.AppendUint16(b, 1)
}

func TestFullEngineQueueDropsWithoutRefusedAndKeepsAccountsFair(t *testing.T) {
	s := testStore(t)
	first, err := s.Create(ClientInput{Name: "first", Engines: "cotten", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(ClientInput{Name: "second", Engines: "cotten", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(context.Background(), s, &resiliencePacketConn{})
	t.Cleanup(m.cancel)
	if m.cfg, err = s.Settings(); err != nil {
		t.Fatal(err)
	}
	m.zones[first.Zone] = &accountRuntime{client: first}
	m.zones[second.Zone] = &accountRuntime{client: second}
	m.cottenJobs = make(chan cottenJob, 2*maxAccountDNSJobs)
	send := func(c Client) (replied bool, answer []byte) {
		m.routeDNS(regressionQuery("x.cot."+c.Zone), nil, false, func(r []byte) { replied, answer = true, r })
		return
	}
	for range maxAccountDNSJobs {
		if replied, _ := send(first); replied {
			t.Fatal("queued job answered")
		}
	}
	replied, answer := send(first)
	if !replied || len(answer) != 0 {
		t.Fatal("noisy account's overflow must be dropped silently", replied, rcodeOf(answer))
	}
	if replied, _ := send(second); replied {
		t.Fatal("noisy account starved another account")
	}
	if d := m.diagnostics.cottenQueueDrops.Load(); d != 1 {
		t.Fatal("drop not counted", d)
	}
	job := <-m.cottenJobs
	job.rt.dnsQueued.Add(-1)
	if replied, _ := send(first); replied {
		t.Fatal("drained slot was not returned to the account")
	}
}

func rcodeOf(r []byte) int {
	if len(r) < 4 {
		return -1
	}
	return int(r[3] & 15)
}

// A SOCKS5 proxy with TCP CONNECT only, answering framed DNS on the tunnel.
func tcpOnlySocks(t *testing.T) (string, <-chan byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	commands := make(chan byte, 32)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				var greeting [3]byte
				if _, err := io.ReadFull(conn, greeting[:]); err != nil {
					return
				}
				conn.Write([]byte{5, 0})
				var header [4]byte
				if _, err := io.ReadFull(conn, header[:]); err != nil {
					return
				}
				if _, err := readSocksAddress(conn, header[3]); err != nil {
					return
				}
				commands <- header[1]
				if header[1] != 1 {
					conn.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				var size [2]byte
				if _, err := io.ReadFull(conn, size[:]); err != nil {
					return
				}
				query := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := io.ReadFull(conn, query); err != nil {
					return
				}
				var message dnsmessage.Message
				if err := message.Unpack(query); err != nil {
					return
				}
				message.Response = true
				if q := message.Questions[0]; q.Type == dnsmessage.TypeA {
					message.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: q.Class, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{93, 184, 216, 34}}}}
				}
				answer, err := message.Pack()
				if err != nil {
					return
				}
				conn.Write(binary.BigEndian.AppendUint16(nil, uint16(len(answer))))
				conn.Write(answer)
			}()
		}
	}()
	return ln.Addr().String(), commands
}

func TestProxyLookupIsCachedAndDoesNotReopenTunnels(t *testing.T) {
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "a", Engines: "cotten", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	address, commands := tcpOnlySocks(t)
	cfg, _ := s.Egress()
	cfg.Cotten = EngineEgress{Mode: "socks5", Address: address, Ports: "80,443"}
	if err = s.SaveEgress(cfg); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, s, nil)
	rt := m.accounts[c.ID]
	first, err := m.lookupViaProxy(context.Background(), rt, upstreamCotten, "example.com")
	if err != nil || len(first) == 0 {
		t.Fatal(first, err)
	}
	opened := len(commands)
	second, err := m.lookupViaProxy(context.Background(), rt, upstreamCotten, "example.com")
	if err != nil || len(second) == 0 || second[0] != first[0] {
		t.Fatal(second, err)
	}
	if len(commands) != opened {
		t.Fatal("cached lookup opened proxy tunnels", opened, len(commands))
	}
}

func TestLoginTableExpiresBeforeApplyingItsCap(t *testing.T) {
	h := &HTTP{attempts: map[string]loginAttempt{}, sessions: map[string]loginSession{}}
	past := time.Now().Add(-time.Hour)
	for i := range 1024 {
		h.attempts[fmt.Sprintf("198.51.100.%d-%d", i/256, i%256)] = loginAttempt{count: 1, until: past}
	}
	r := httptest.NewRequest("POST", "/api/login", nil)
	r.RemoteAddr = "203.0.113.9:4000"
	w := httptest.NewRecorder()
	if _, allowed := h.authAttempt(w, r); !allowed {
		t.Fatal("expired entries locked out a new address", w.Code)
	}
	if len(h.attempts) != 1 {
		t.Fatal("expired entries retained", len(h.attempts))
	}
	for range 7 {
		if _, allowed := h.authAttempt(httptest.NewRecorder(), r); !allowed {
			t.Fatal("limit reached early")
		}
	}
	if _, allowed := h.authAttempt(httptest.NewRecorder(), r); allowed {
		t.Fatal("ninth attempt allowed")
	}
}

func TestDNSControlAnswersNodataInsideZoneOnly(t *testing.T) {
	const base = "soar.example.org"
	for _, tc := range []struct {
		name  string
		typ   uint16
		rcode int
	}{
		{base, 1, 0},
		{"x.unknown." + base, 16, 0},
		{"unknown.example.net", 16, 5},
	} {
		q := make([]byte, 12)
		q[5] = 1
		q = append(q, dnsName(tc.name)...)
		q = binary.BigEndian.AppendUint16(q, tc.typ)
		q = binary.BigEndian.AppendUint16(q, 1)
		name, end, typ, ok := dnsQuestion(q)
		if !ok {
			t.Fatal(tc.name)
		}
		zone := base
		if tc.rcode == 5 {
			zone = ""
		}
		r := dnsControl(q, end, name, typ, zone, "ns1.example.org")
		if rcodeOf(r) != tc.rcode || binary.BigEndian.Uint16(r[6:8]) != 0 {
			t.Fatal("wrong answer", tc.name, rcodeOf(r))
		}
		if tc.rcode == 0 && binary.BigEndian.Uint16(r[8:10]) != 1 {
			t.Fatal("NODATA without SOA", tc.name)
		}
	}
}

func TestSettingsSaveWithoutChangesKeepsEnginesRunning(t *testing.T) {
	b, s, h := testBrowser(t)
	c, err := s.Create(ClientInput{Name: "keep", Engines: "soar", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.manager.Reconcile(); err != nil {
		t.Fatal(err)
	}
	h.manager.mu.RLock()
	old := h.manager.accounts[c.ID]
	h.manager.mu.RUnlock()
	b.login()
	cfg, _ := s.Settings()
	b.request("/api/settings", "PUT", cfg, 200)
	if old.ctx.Err() != nil {
		t.Fatal("unchanged settings dropped tunnels")
	}
}

func TestTunnelDNSFallsBackToTCPWhenSocksHasNoUDP(t *testing.T) {
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "a", Engines: "cotten", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	address, commands := tcpOnlySocks(t)
	cfg, _ := s.Egress()
	cfg.Cotten = EngineEgress{Mode: "socks5", Address: address, Ports: "80,443"}
	if err = s.SaveEgress(cfg); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, s, nil)
	query := regressionQuery("example.com")
	response, err := m.tunnelDNS(m.accounts[c.ID], context.Background(), query, upstreamCotten)
	if err != nil {
		t.Fatal("TCP fallback missing:", err)
	}
	if len(response) < 12 || response[0] != query[0] || response[1] != query[1] {
		t.Fatal("bad response", response)
	}
	if first, second := <-commands, <-commands; first != 3 || second != 1 {
		t.Fatal("expected UDP ASSOCIATE then CONNECT", first, second)
	}
}
