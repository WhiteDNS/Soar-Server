package panel

import (
	"context"
	"io"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestSettingsSaveRestartsEnginesAndKeepsAccountData(t *testing.T) {
	b, s, h := testBrowser(t)
	c, err := s.Create(ClientInput{Name: "settings restart", Engines: "all", Enabled: true, QuotaBytes: 1 << 20, StreamLimit: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.manager.Reconcile(); err != nil {
		t.Fatal(err)
	}
	m := h.manager
	m.mu.RLock()
	old := m.accounts[c.ID]
	m.mu.RUnlock()
	if _, err = m.masterDNSFor(old); err != nil {
		t.Fatal(err)
	}
	if _, err = m.cottenFor(old); err != nil {
		t.Fatal(err)
	}
	upstream, peer := net.Pipe()
	defer peer.Close()
	m.dialOverride = func(context.Context, string, string) (net.Conn, error) { return upstream, nil }
	conn, err := m.dialForEngine(old, context.Background(), "tcp", "8.8.8.8:443", upstreamMasterDNS)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	before, _ := s.Get(c.ID)
	profiles, _ := s.Profiles(c.ID)
	token, _ := s.SubscriptionToken(c.ID, false)
	b.login()
	cfg, _ := s.Settings()
	invalid := cfg
	invalid.Resolvers = []string{"not a resolver"}
	b.request("/api/settings", "PUT", invalid, 400)
	m.mu.RLock()
	same := m.accounts[c.ID] == old
	m.mu.RUnlock()
	if !same || old.ctx.Err() != nil {
		t.Fatal("failed save restarted an engine")
	}
	cfg.Resolvers = []string{"9.9.9.9"}
	b.request("/api/settings", "PUT", cfg, 200)
	m.mu.RLock()
	current := m.accounts[c.ID]
	resolvers := append([]string(nil), m.cfg.Resolvers...)
	m.mu.RUnlock()
	if current == nil || current == old || old.ctx.Err() == nil {
		t.Fatal("successful settings save retained the old engine runtime")
	}
	if !reflect.DeepEqual(resolvers, cfg.Resolvers) {
		t.Fatal("restarted engine has stale settings", resolvers)
	}
	peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatal("settings save did not close the old stream", err)
	}
	after, _ := s.Get(c.ID)
	if before != after {
		t.Fatal("engine restart changed persisted account data")
	}
	afterProfiles, _ := s.Profiles(c.ID)
	// Resolver changes update Soar profiles; account secrets and MasterDNS keys stay fixed.
	if profiles["masterdns"] != afterProfiles["masterdns"] {
		t.Fatal("settings restart changed the MasterDNS key")
	}
	account, _, err := s.Subscription(token)
	if err != nil || account.ID != c.ID {
		t.Fatal("settings restart changed subscription identity", err)
	}
	fresh, err := m.masterDNSFor(current)
	if err != nil || fresh == nil {
		t.Fatal("MasterDNS could not restart", err)
	}
	if err = m.Reconcile(); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	same = m.accounts[c.ID] == current
	m.mu.RUnlock()
	if !same {
		t.Fatal("routine reconciliation unnecessarily restarted engines")
	}
}
