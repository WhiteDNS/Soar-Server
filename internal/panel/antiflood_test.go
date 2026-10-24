package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func floodFixture(t *testing.T, p AntiFloodPolicy) (*floodGuard, *Store, Client, time.Time) {
	t.Helper()
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "Protection fixture", Enabled: true, Engines: "all"})
	if err != nil {
		t.Fatal(err)
	}
	g := newFloodGuard(s)
	if err := g.configure(p, []Client{c}); err != nil {
		t.Fatal(err)
	}
	return g, s, c, time.Now()
}

func TestAntiFloodBucketAndRollingWindows(t *testing.T) {
	now := time.Now()
	var b floodBucket
	if !b.available(now, 2, 4, 4) {
		t.Fatal("initial burst missing")
	}
	b.tokens -= 4
	if b.available(now, 2, 4, 1) || b.available(now.Add(-time.Second), 2, 4, 1) {
		t.Fatal("empty or backwards clock refilled")
	}
	if !b.available(now.Add(500*time.Millisecond), 2, 4, 1) {
		t.Fatal("fractional refill failed")
	}
	b.tokens--
	if b.available(now.Add(500*time.Millisecond), 2, 1, 2) {
		t.Fatal("cost above burst allowed")
	}
	var w floodWindow
	w.add(now)
	w.add(now.Add(time.Second))
	if w.count(now.Add(59*time.Second), 60) != 2 || w.count(now.Add(60*time.Second), 60) != 1 || w.count(now.Add(61*time.Second), 60) != 0 {
		t.Fatal("window boundary failed")
	}
	want := netip.MustParseAddr("192.0.2.1")
	for _, address := range []string{"192.0.2.1", "::ffff:192.0.2.1", "64:ff9b::c000:201", "2002:c000:201::1"} {
		if floodTargetIP(netip.MustParseAddr(address)) != want {
			t.Fatal("encoded IP bypasses target budget", address)
		}
	}
}

func TestAntiFloodAtomicAdmissionAndRelease(t *testing.T) {
	p := DefaultAntiFloodPolicy()
	p.AccountBurst = 7
	p.AccountRate = 1
	g, _, c, now := floodFixture(t, p)
	var wg sync.WaitGroup
	var admitted atomic.Int32
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err, _ := g.begin(c.ID, false, now)
			if err == nil {
				admitted.Add(1)
				l.release()
				l.release()
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 7 || g.snapshot().Active != 0 {
		t.Fatal("concurrent admission exceeded burst or leaked", admitted.Load(), g.snapshot())
	}
	l, err, _ := g.begin(c.ID, false, now.Add(time.Second))
	if err != nil {
		t.Fatal("rate failed to refill", err)
	}
	l.release()
	if g.snapshot().Denied != 93 {
		t.Fatal("denial count lost")
	}
}

func TestAntiFloodTargetNetworkAndPendingCaps(t *testing.T) {
	for _, kind := range []string{"target", "network", "account_streams", "server_streams"} {
		t.Run(kind, func(t *testing.T) {
			p := DefaultAntiFloodPolicy()
			p.TargetBurst = 1
			p.NetworkBurst = 1
			if kind == "account_streams" {
				p.AccountStreams = 1
			}
			if kind == "server_streams" {
				p.ServerStreams = 1
			}
			g, s, c, now := floodFixture(t, p)
			other, err := s.Create(ClientInput{Name: "second", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			first, err, _ := g.begin(c.ID, false, now)
			if err != nil {
				t.Fatal(err)
			}
			defer first.release()
			if kind == "target" || kind == "network" {
				if err, _ := g.target(first, netip.MustParseAddr("192.0.2.1"), now); err != nil {
					t.Fatal(err)
				}
			}
			id := other.ID
			if kind == "account_streams" {
				id = c.ID
			}
			second, err, _ := g.begin(id, false, now)
			if kind == "account_streams" || kind == "server_streams" {
				if !errors.Is(err, errAntiFlood) {
					t.Fatal("pending sockets bypassed cap", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer second.release()
			ip := "::ffff:192.0.2.1"
			if kind == "network" {
				ip = "192.0.2.2"
			}
			if err, _ := g.target(second, netip.MustParseAddr(ip), now); !errors.Is(err, errAntiFlood) {
				t.Fatal("cross-account target/network cap bypassed", err)
			}
			if len(g.blocks) != 0 {
				t.Fatal("shared destination pressure quarantined account")
			}
		})
	}
	if floodNetwork(netip.MustParseAddr("2001:db8:1::1")) != floodNetwork(netip.MustParseAddr("2001:db8:1::ffff")) {
		t.Fatal("IPv6 /64 not aggregated")
	}
}

func TestAntiFloodScanQuarantinePersistenceExpiryAndRelease(t *testing.T) {
	p := DefaultAntiFloodPolicy()
	p.TargetsPerMinute = 10
	p.ViolationThreshold = 10
	p.CooldownSeconds = 30
	g, s, c, now := floodFixture(t, p)
	for i := range 10 {
		l, err, _ := g.begin(c.ID, false, now)
		if err != nil {
			t.Fatal(err)
		}
		if err, _ := g.target(l, netip.AddrFrom4([4]byte{192, 0, 2, byte(i + 1)}), now); err != nil {
			t.Fatal(err)
		}
		l.release()
	}
	for i := range 10 {
		l, err, _ := g.begin(c.ID, false, now)
		if err != nil {
			t.Fatal(err)
		}
		err, q := g.target(l, netip.MustParseAddr("198.51.100.1"), now)
		l.release()
		if !errors.Is(err, errAntiFlood) || q != (i == 9) {
			t.Fatal("scan escalation failed", i, err, q)
		}
	}
	if !g.blocked(c.ID) {
		t.Fatal("account not quarantined")
	}
	restarted := newFloodGuard(s)
	if err := restarted.configure(p, []Client{c}); err != nil {
		t.Fatal(err)
	}
	if !restarted.blocked(c.ID) {
		t.Fatal("restart erased quarantine")
	}
	if _, err, _ := restarted.begin(c.ID, false, now); !errors.Is(err, errAntiFlood) {
		t.Fatal("persistent quarantine bypassed")
	}
	path := filepath.Join(t.TempDir(), "protection.db")
	var backup bytes.Buffer
	if err := s.BackupContext(context.Background(), &backup); err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(path, backup.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectBackupFile(path); err != nil {
		t.Fatal("backup with protection not restorable", err)
	}
	l, err, _ := restarted.begin(c.ID, false, now.Add(31*time.Second))
	if err != nil {
		t.Fatal("expired quarantine did not release", err)
	}
	l.release()
	restarted.mu.Lock()
	restarted.quarantineLocked(c.ID, "target_scan", now.Add(31*time.Second))
	block := restarted.blocks[c.ID]
	restarted.mu.Unlock()
	if block.Strikes != 2 || block.Until-block.At != 60000 {
		t.Fatal("repeat offense did not escalate", block)
	}
	if err := restarted.reset(c.ID); err != nil {
		t.Fatal(err)
	}
	third := newFloodGuard(s)
	if err := third.configure(p, []Client{c}); err != nil {
		t.Fatal(err)
	}
	if third.blocked(c.ID) {
		t.Fatal("manual release was not persisted")
	}
	after, _ := s.Get(c.ID)
	if c != after {
		t.Fatal("protection mutated account identity or usage")
	}
}

func TestAntiFloodObserveOffAndLiveUploadPolicy(t *testing.T) {
	p := DefaultAntiFloodPolicy()
	p.Mode = "monitor"
	p.AccountBurst = 1
	p.AccountRate = 1
	p.ViolationThreshold = 10
	g, _, c, now := floodFixture(t, p)
	for range 20 {
		l, err, _ := g.begin(c.ID, false, now)
		if err != nil {
			t.Fatal("monitor blocked", err)
		}
		l.release()
	}
	if s := g.snapshot(); s.Observed != 19 || s.Denied != 0 || s.Quarantines != 0 {
		t.Fatal("monitor semantics wrong", s)
	}
	p.Mode = "off"
	p.AccountUploadKiB = 32
	if err := g.configure(p, []Client{c}); err != nil {
		t.Fatal(err)
	}
	l, err, _ := g.begin(c.ID, false, now)
	if err != nil {
		t.Fatal(err)
	}
	defer l.release()
	if err, _ := g.target(l, netip.MustParseAddr("192.0.2.1"), now); err != nil {
		t.Fatal(err)
	}
	if err, _ := g.write(l, 65536, now); err != nil {
		t.Fatal("off blocked upload")
	}
	p.Mode = "enforce"
	if err := g.configure(p, []Client{c}); err != nil {
		t.Fatal(err)
	}
	if err, _ := g.write(l, 65536, now); err != nil {
		t.Fatal("burst denied", err)
	}
	if err, _ := g.write(l, 1, now); !errors.Is(err, errAntiFlood) {
		t.Fatal("old connection escaped new upload policy")
	}
	if err, _ := g.write(l, 32768, now.Add(time.Second)); err != nil {
		t.Fatal("upload bucket failed to refill", err)
	}
}

func TestAntiFloodFailuresRequireDiverseTargets(t *testing.T) {
	p := DefaultAntiFloodPolicy()
	p.FailureThreshold = 10
	g, _, c, now := floodFixture(t, p)
	for i := range 20 {
		l, err, _ := g.begin(c.ID, false, now)
		if err != nil {
			t.Fatal(err)
		}
		ip := netip.MustParseAddr("192.0.2.1")
		if i >= 10 {
			ip = netip.AddrFrom4([4]byte{192, 0, 2, byte(i)})
		}
		if err, _ := g.target(l, ip, now); err != nil {
			t.Fatal(err)
		}
		q := g.outcome(l, true, now)
		l.release()
		if i < 18 && q {
			t.Fatal("unavailable site falsely quarantined account")
		}
		if i == 18 && !q {
			t.Fatal("high-failure multi-target scan was not quarantined")
		}
		if q {
			break
		}
	}
}

func TestAntiFloodResolverIsolationAndBoundedTables(t *testing.T) {
	p := DefaultAntiFloodPolicy()
	p.AccountRate = 1
	p.AccountBurst = 1
	p.ResolverQPS = 10
	g, _, c, now := floodFixture(t, p)
	for range 20 {
		l, err, _ := g.begin(c.ID, true, now)
		if err != nil {
			t.Fatal("normal DNS shared outbound connection budget", err)
		}
		l.release()
	}
	if _, err, _ := g.begin(c.ID, true, now); !errors.Is(err, errAntiFlood) {
		t.Fatal("DNS burst not limited")
	}
	if g.blocked(c.ID) {
		t.Fatal("DNS retries quarantined account")
	}
	l, err, _ := g.begin(c.ID, false, now)
	if err != nil {
		t.Fatal(err)
	}
	defer l.release()
	g.mu.Lock()
	for i := range maxFloodTargets {
		g.targets[netip.AddrFrom4([4]byte{192, byte(i >> 8), byte(i), 1})] = &floodDestination{lastSeen: now}
	}
	g.mu.Unlock()
	if err, _ := g.target(l, netip.MustParseAddr("198.51.100.1"), now); !errors.Is(err, errAntiFlood) {
		t.Fatal("full target table failed open")
	}
	if len(g.targets) > maxFloodTargets || g.blocked(c.ID) {
		t.Fatal("unbounded state or false quarantine")
	}
	if err, _ := g.target(l, netip.MustParseAddr("198.51.100.1"), now.Add(61*time.Second)); err != nil {
		t.Fatal("idle full buckets did not expire", err)
	}
	if len(g.targets) != 1 {
		t.Fatal("expired buckets retained", len(g.targets))
	}
}

func TestAntiFloodAPICompatibilityAuthenticationAndBackupValidation(t *testing.T) {
	b, s, h := testBrowser(t)
	b.request("/api/antiflood", "GET", nil, 401)
	b.login()
	var status FloodStatus
	if err := json.Unmarshal(b.request("/api/antiflood", "GET", nil, 200), &status); err != nil || status.Policy.Mode != "enforce" {
		t.Fatal("old database did not receive protected defaults", err)
	}
	p := status.Policy
	p.AccountRate = 8
	b.request("/api/antiflood", "PUT", p, 200)
	var legacy map[string]any
	cfg, _ := s.Settings()
	raw, _ := json.Marshal(cfg)
	json.Unmarshal(raw, &legacy)
	delete(legacy, "antiflood")
	b.request("/api/settings", "PUT", legacy, 200)
	if saved, _ := s.Settings(); saved.AntiFlood.AccountRate != 8 {
		t.Fatal("legacy settings save erased protection")
	}
	p.AccountRate = 0
	b.request("/api/antiflood", "PUT", p, 400)
	csrf := b.csrf
	b.csrf = ""
	b.request("/api/antiflood", "PUT", status.Policy, 403)
	b.csrf = csrf
	c := account(t, s, 0)
	h.manager.flood.mu.Lock()
	h.manager.flood.quarantineLocked(c.ID, "account_rate", time.Now())
	h.manager.flood.mu.Unlock()
	b.request("/api/antiflood/release", "POST", map[string]string{"client_id": c.ID}, 200)
	if h.manager.flood.blocked(c.ID) {
		t.Fatal("API did not release account")
	}
	b.request("/api/antiflood/release", "POST", map[string]string{"client_id": "missing"}, 404)
	if err := s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(meta).Put(floodBlockKey, []byte(`{"bad":{"until":-1}}`)) }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.floodBlocks(); err == nil {
		t.Fatal("corrupt quarantine accepted")
	}
}

func TestAntiFloodPinnedEgressSharedAcrossEnginesAndAliases(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	cfg, _ := s.Settings()
	p := DefaultAntiFloodPolicy()
	p.TargetBurst = 1
	p.TargetRate = 1
	cfg.AntiFlood = &p
	if err := s.SaveSettings(cfg); err != nil {
		t.Fatal(err)
	}
	var dials atomic.Int32
	m := testManager(t, s, func(_ context.Context, _, address string) (net.Conn, error) {
		if address != "192.0.2.1:443" {
			t.Errorf("hostname was not pinned: %s", address)
		}
		dials.Add(1)
		local, peer := net.Pipe()
		go func() { defer peer.Close(); io.Copy(io.Discard, peer) }()
		return local, nil
	})
	m.lookupOverride = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
	}
	rt := m.accounts[c.ID]
	conn, err := m.dialForEngine(rt, context.Background(), "tcp", "one.example:443", upstreamSoar)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	for _, engine := range []upstreamEngine{upstreamCotten, upstreamMasterDNS} {
		_, err = m.dialForEngine(rt, context.Background(), "tcp", "two.example:443", engine)
		if !errors.Is(err, errAntiFlood) || dials.Load() != 1 {
			t.Fatal("alias/engine rotation bypassed actual target cap", engine, err, dials.Load())
		}
	}
	if m.active.Load() != 0 || m.flood.snapshot().Active != 0 {
		t.Fatal("rejected dial leaked admission")
	}
}

func TestAntiFloodPrivateResolutionAndCanceledDialsNeverLeaveHost(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	m := testManager(t, s, nil)
	m.lookupOverride = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	if conn, err := m.dial(m.accounts[c.ID], context.Background(), "tcp", "alias.example:443"); err == nil {
		conn.Close()
		t.Fatal("private resolution was dialed")
	}
	if m.active.Load() != 0 || m.flood.snapshot().Active != 0 {
		t.Fatal("private rejection leaked admission")
	}
	var dialed atomic.Bool
	m.dialOverride = func(context.Context, string, string) (net.Conn, error) {
		dialed.Store(true)
		return nil, errors.New("must not dial")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.dial(m.accounts[c.ID], ctx, "tcp", "alias.example:443")
	if dialed.Load() {
		t.Fatal("canceled operation created network traffic")
	}
}

func TestAntiFloodRejectsBytesBeforeWriteAndDisconnectsAllEngines(t *testing.T) {
	for _, scenario := range []string{"upload", "quarantine"} {
		t.Run(scenario, func(t *testing.T) {
			s := testStore(t)
			c := account(t, s, 0)
			cfg, _ := s.Settings()
			p := DefaultAntiFloodPolicy()
			p.AccountUploadKiB = 32
			p.AccountRate = 1
			p.AccountBurst = 3
			p.ViolationThreshold = 10
			cfg.AntiFlood = &p
			if err := s.SaveSettings(cfg); err != nil {
				t.Fatal(err)
			}
			completed := make(chan int64, 3)
			m := testManager(t, s, func(context.Context, string, string) (net.Conn, error) {
				local, peer := net.Pipe()
				go func() { defer peer.Close(); n, _ := io.Copy(io.Discard, peer); completed <- n }()
				return local, nil
			})
			m.lookupOverride = func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
			}
			rt := m.accounts[c.ID]
			first, err := m.dialForEngine(rt, context.Background(), "tcp", "one.example:443", upstreamSoar)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "upload" {
				n, err := first.Write(make([]byte, 1<<20))
				if !errors.Is(err, errAntiFlood) || n == 1<<20 {
					t.Fatal("oversize upload not rejected", n, err)
				}
				select {
				case written := <-completed:
					if written != int64(n) {
						t.Fatal("bytes escaped admission", written, n)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("upload rejection did not close socket")
				}
				info, _ := s.Get(c.ID)
				if info.UploadBytes != int64(n) {
					t.Fatal("rejected bytes were billed", info.UploadBytes, n)
				}
			} else {
				second, err := m.dialForEngine(rt, context.Background(), "tcp", "two.example:443", upstreamCotten)
				if err != nil {
					t.Fatal(err)
				}
				third, err := m.dialForEngine(rt, context.Background(), "tcp", "three.example:443", upstreamMasterDNS)
				if err != nil {
					t.Fatal(err)
				}
				defer first.Close()
				defer second.Close()
				defer third.Close()
				for range 10 {
					if _, err := m.dial(rt, context.Background(), "tcp", "three.example:443"); !errors.Is(err, errAntiFlood) {
						t.Fatal("rate bypass", err)
					}
				}
				for range 3 {
					select {
					case <-completed:
					case <-time.After(2 * time.Second):
						t.Fatal("quarantine left an engine socket open")
					}
				}
				if !m.flood.blocked(c.ID) || rt.ctx.Err() == nil {
					t.Fatal("quarantine did not retire authenticated runtime")
				}
			}
			if m.active.Load() != 0 || m.flood.snapshot().Active != 0 {
				t.Fatal("enforcement leaked socket capacity")
			}
		})
	}
}

func TestAntiFloodAddressFallbackReleasesFailedTarget(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	m := testManager(t, s, func(_ context.Context, _, address string) (net.Conn, error) {
		if address == "192.0.2.1:443" {
			return nil, errors.New("simulated address failure")
		}
		local, peer := net.Pipe()
		go func() { defer peer.Close(); io.Copy(io.Discard, peer) }()
		return local, nil
	})
	m.lookupOverride = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")}, nil
	}
	conn, err := m.dial(m.accounts[c.ID], context.Background(), "tcp", "fallback.example:443")
	if err != nil {
		t.Fatal(err)
	}
	m.flood.mu.Lock()
	failed := m.flood.targets[netip.MustParseAddr("192.0.2.1")].active
	success := m.flood.targets[netip.MustParseAddr("192.0.2.2")].active
	m.flood.mu.Unlock()
	if failed != 0 || success != 1 {
		t.Fatal("address fallback leaked or missed target reservation", failed, success)
	}
	conn.Close()
	if m.flood.snapshot().Active != 0 {
		t.Fatal("fallback leaked account reservation")
	}
}
