package panel

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
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
				query[2] |= 0x80
				conn.Write(binary.BigEndian.AppendUint16(nil, uint16(len(query))))
				conn.Write(query)
			}()
		}
	}()
	return ln.Addr().String(), commands
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
