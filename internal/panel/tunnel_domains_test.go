package panel

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTunnelDomainsPreserveIdentityAndSubscriptions(t *testing.T) {
	b, s, h := testBrowser(t)
	b.login()
	c, err := s.Create(ClientInput{Name: "multi domain", Engines: "both", Enabled: true, StreamLimit: 8, QuotaBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := s.Seed(c.ID)
	token, _ := s.SubscriptionToken(c.ID, false)
	before, _ := s.Profiles(c.ID)
	b.request("/api/tunnel-domains", "PUT", map[string]any{"zones": []string{"soar.example.org", "t.example.net"}, "primary": "soar.example.org"}, 200)
	cfg, _ := s.Settings()
	alias := cfg.accountZone(c, "t.example.net")
	if len(alias) > 45 {
		t.Fatal("alias exceeds conservative Soar zone limit")
	}
	after, _ := s.Profiles(c.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("adding alias changed original links")
	}
	link := "/sub/" + token + "?format=plain"
	lines := strings.Fields(string(b.request(link, "GET", nil, 200)))
	if len(lines) != 4 {
		t.Fatal("subscription does not contain both engines on both domains", len(lines))
	}
	ids := map[string]bool{}
	for _, line := range lines {
		_, encoded, _ := strings.Cut(line, "://")
		raw, _ := base64.RawURLEncoding.DecodeString(encoded)
		var body struct {
			Profile struct {
				ID     string
				Server struct{ Domain string }
			}
		}
		if json.Unmarshal(raw, &body) != nil || ids[body.Profile.ID] {
			t.Fatal("unstable or duplicate profile ID")
		}
		ids[body.Profile.ID] = true
	}
	h.manager.mu.RLock()
	runtime := h.manager.accounts[c.ID]
	h.manager.mu.RUnlock()
	b.request("/api/tunnel-domains", "PUT", map[string]any{"zones": cfg.TunnelZones, "primary": "t.example.net"}, 200)
	h.manager.mu.RLock()
	sameRuntime := h.manager.accounts[c.ID] == runtime
	h.manager.mu.RUnlock()
	if !sameRuntime {
		t.Fatal("changing preference disconnected live sessions")
	}
	preferred, _ := s.Profiles(c.ID)
	if reflect.DeepEqual(preferred, before) {
		t.Fatal("primary domain did not change direct links")
	}
	updated, _ := s.Get(c.ID)
	newSeed, _ := s.Seed(c.ID)
	newToken, _ := s.SubscriptionToken(c.ID, false)
	if !reflect.DeepEqual(updated, c) || !bytes.Equal(seed, newSeed) || token != newToken {
		t.Fatal("domain change changed identity, counters or subscription credential")
	}
	// A pre-0.5 settings caller must preserve the domain configuration.
	old := cfg
	old.TunnelZones = nil
	old.PrimaryZone = ""
	if err = s.SaveSettings(old); err != nil {
		t.Fatal(err)
	}
	actual, _ := s.Settings()
	if actual.primaryZone() != "t.example.net" {
		t.Fatal("old settings request erased domains")
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	file, e := os.Create(backup)
	if e != nil {
		t.Fatal(e)
	}
	err = s.Backup(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = InspectBackupFile(backup); err != nil {
		t.Fatal(err)
	}
	restored, e := OpenStore(backup)
	if e != nil {
		t.Fatal(e)
	}
	restoredSettings, e := restored.Settings()
	if e != nil {
		t.Fatal(e)
	}
	restoredLinks, e := restored.Profiles(c.ID)
	restoredToken, _ := restored.SubscriptionToken(c.ID, false)
	restored.Close()
	if e != nil || restoredSettings.primaryZone() != actual.primaryZone() || !reflect.DeepEqual(restoredLinks, preferred) || restoredToken != token {
		t.Fatal("restore changed domains, profiles or subscription", e)
	}
	b.request("/api/tunnel-domains", "PUT", map[string]any{"zones": []string{"t.example.net"}, "primary": "t.example.net"}, 200)
	h.manager.mu.RLock()
	oldRT := h.manager.zones[c.Zone]
	newRT := h.manager.zones[alias]
	h.manager.mu.RUnlock()
	if oldRT != nil || newRT == nil {
		t.Fatal("removing old domain left it routed, or removed the alias")
	}
	b.request("/api/tunnel-domains", "PUT", map[string]any{"zones": []string{}, "primary": ""}, 400)
	b.request("/api/tunnel-domains", "PUT", map[string]any{"zones": []string{"t.example.net", "x.t.example.net"}, "primary": "t.example.net"}, 400)
}

func TestTunnelAliasSoarAndDNSProof(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 1<<20)
	cfg, _ := s.Settings()
	cfg.TunnelZones = []string{cfg.BaseZone, "t.example.net"}
	cfg.PrimaryZone = "t.example.net"
	if err := s.SaveSettings(cfg); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, s, func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() { defer b.Close(); io.Copy(b, b) }()
		return a, nil
	})
	for _, zone := range cfg.accountZones(c) {
		alias := c
		alias.Zone = zone
		client := nativeClient(t, m, alias)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, err := client.DialContext(ctx, "tcp", "8.8.8.8:443")
		cancel()
		if err != nil {
			t.Fatal(zone, err)
		}
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		payload := []byte("same account, alternate domain")
		go conn.Write(payload)
		reply := make([]byte, len(payload))
		_, err = io.ReadFull(conn, reply)
		conn.Close()
		if err != nil || !bytes.Equal(payload, reply) {
			t.Fatal("alias stream failed", err)
		}
	}
	actual, _ := s.Get(c.ID)
	if actual.Used() != int64(4*len("same account, alternate domain")) {
		t.Fatal("aliases did not share exact accounting", actual.Used())
	}
	if err := checkTunnelRoute(context.Background(), "t.example.net", []string{m.pc.LocalAddr().String()}, m.domainProof); err != nil {
		t.Fatal(err)
	}
	other := NewManager(context.Background(), s, nil)
	defer other.cancel()
	if err := checkTunnelRoute(context.Background(), "t.example.net", []string{m.pc.LocalAddr().String()}, other.domainProof); err == nil {
		t.Fatal("another panel's proof accepted")
	}
}

func TestLegacyNameserverIsNeverGuessed(t *testing.T) {
	cfg := Settings{BaseZone: "t.example.org"}
	if cfg.nameserverFor(cfg.BaseZone) != "" {
		t.Fatal("guessed NS can poison a working legacy delegation")
	}
	if cfg.nameserverFor("t.example.net") != "ns1.example.net" {
		t.Fatal("backup nameserver depends on original domain")
	}
	cfg.NameserverHost = "custom.example.org"
	if cfg.nameserverFor(cfg.BaseZone) != cfg.NameserverHost {
		t.Fatal("saved original nameserver was lost")
	}
	cfg.TunnelNameservers = map[string]string{"t.example.net": "custom.example.net"}
	if cfg.nameserverFor("t.example.net") != "custom.example.net" {
		t.Fatal("explicit backup nameserver ignored")
	}
}

func TestPanelAliasesCertificatesAndHostGate(t *testing.T) {
	a, err := NewAccessController(filepath.Join(t.TempDir(), "access.json"))
	if err != nil {
		t.Fatal(err)
	}
	a.lookup = func(context.Context, string) ([]string, error) { return []string{"203.0.113.1"}, nil }
	a.cfg = AccessConfig{HTTPS: true, Domain: "panel.example.org", BasePath: "/abcdefghijklmnopqrstuvwxyz012345/"}
	a.issue = func(host string) error {
		if host == "blocked.example.net" {
			return errors.New("certificate fixture failure")
		}
		return nil
	}
	in := AccessInput{HTTPS: true, Domain: "panel.example.org", Aliases: []string{"panel.example.net", "blocked.example.net"}, BasePath: a.cfg.BasePath}
	prepare := func() {
		t.Helper()
		if _, err = a.Prepare("https://panel.example.org", "owner", in, nil); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 100 && a.Plan().State == "preparing"; i++ {
			time.Sleep(time.Millisecond)
		}
	}
	prepare()
	if a.Plan().State != "failed" || len(a.Config().Aliases) != 0 {
		t.Fatal("partial certificate failure changed current domains")
	}
	in.Aliases = []string{"panel.example.net"}
	prepare()
	if a.Plan().State != "ready" {
		t.Fatal(a.Plan())
	}
	if _, err = a.Apply(a.Plan().ID, "owner"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := ReadAccessConfig(a.file)
	if err != nil || !reloaded.hasDomain("panel.example.net") {
		t.Fatal("alias not persisted", err)
	}
	_, _, h := testBrowser(t)
	h.SetAccess(a)
	for _, host := range []string{"panel.example.org", "panel.example.net", "unconfigured.example.net"} {
		for _, secure := range []bool{false, true} {
			r := httptest.NewRequest("GET", "http://"+host+a.Config().BasePath+"api/session", nil)
			r.RemoteAddr = "203.0.113.2:1234"
			if secure {
				r.TLS = &tls.ConnectionState{}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 404
			if secure && a.Config().hasDomain(host) {
				want = 401
			}
			if w.Code != want {
				t.Fatalf("host %s TLS %v: %d, want %d", host, secure, w.Code, want)
			}
		}
	}
	r := httptest.NewRequest("GET", "https://panel.example.net/", nil)
	r.TLS = &tls.ConnectionState{}
	if got := h.subscriptionURL(r, "credential"); got != "https://panel.example.net/sub/credential" {
		t.Fatal("subscription forced blocked primary host", got)
	}
}
