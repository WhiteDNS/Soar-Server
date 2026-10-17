package panel

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := OpenStore(filepath.Join(t.TempDir(), "panel.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.Init(Settings{BaseZone: "soar.example.org", Resolvers: []string{"1.1.1.1"}, Country: "IR", AllowPorts: []uint16{80, 443}}, "admin", "test-only-password-2026"); e != nil {
		t.Fatal(e)
	}
	return s
}
func account(t *testing.T, s *Store, quota int64) Client {
	t.Helper()
	c, e := s.Create(ClientInput{Name: "Test account", Enabled: true, QuotaBytes: quota, StreamLimit: 16})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func input(c Client) ClientInput {
	return ClientInput{Name: c.Name, Note: c.Note, Enabled: c.Enabled, QuotaBytes: c.QuotaBytes, ExpiresAt: c.ExpiresAt, RateBytes: c.RateBytes, StreamLimit: c.StreamLimit, Revision: c.Revision}
}

func TestConcurrentQuotaAndAccountIsolation(t *testing.T) {
	s := testStore(t)
	a := account(t, s, 10000)
	b := account(t, s, 10000)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _, e := s.reserve(a.ID, 73, j%2 == 0)
				if e != nil && !errors.Is(e, ErrDenied) {
					t.Error(e)
				}
			}
		}()
	}
	wg.Wait()
	actual, _ := s.Get(a.ID)
	other, _ := s.Get(b.ID)
	if actual.Used() != 10000 || other.Used() != 0 {
		t.Fatalf("usage: %d / %d", actual.Used(), other.Used())
	}
	if _, _, e := s.reserve(a.ID, 1, true); !errors.Is(e, ErrDenied) {
		t.Fatal("exhausted account accepted")
	}
}
func TestRefundSurvivesEditButNotReset(t *testing.T) {
	s := testStore(t)
	a := account(t, s, 1000)
	_, epoch, _ := s.reserve(a.ID, 100, true)
	a.Name = "Edited"
	if _, e := s.Edit(a.ID, input(a)); e != nil {
		t.Fatal(e)
	}
	if e := s.refund(a.ID, 40, true, epoch); e != nil {
		t.Fatal(e)
	}
	a, _ = s.Get(a.ID)
	if a.Used() != 60 {
		t.Fatal("metadata edit lost refund")
	}
	if e := s.ResetUsage(a.ID); e != nil {
		t.Fatal(e)
	}
	_, _, _ = s.reserve(a.ID, 50, true)
	_ = s.refund(a.ID, 40, true, epoch)
	a, _ = s.Get(a.ID)
	if a.Used() != 50 {
		t.Fatal("old refund erased new period")
	}
	if _, _, e := s.reserveEpoch(a.ID, 20, true, epoch); !errors.Is(e, ErrDenied) {
		t.Fatal("old connection billed new period")
	}
}
func TestDurableBackupAndExistingProfileFormat(t *testing.T) {
	s := testStore(t)
	a := account(t, s, 1000)
	_, _, _ = s.reserve(a.ID, 100, false)
	var backup bytes.Buffer
	if e := s.Backup(&backup); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "restore.db")
	if e := writePrivate(path, backup.Bytes()); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(path)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	c, e := restored.Get(a.ID)
	if e != nil || c.DownloadBytes != 100 {
		t.Fatal("backup lost usage", e)
	}
	if !restored.CheckAdmin("admin", "test-only-password-2026") {
		t.Fatal("backup lost administrator")
	}
	link, e := restored.Profile(a.ID)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "soar://"))
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Schema  string
		Version int
		Profile struct {
			Server struct {
				Domain    string
				PublicKey string `json:"public_key"`
			}
			Resolvers    []string
			SoarSettings struct {
				Country              string
				Discovery            bool
				Transport            string
				DialTimeoutSeconds   int
				DiscoveryWant        int
				DiscoveryConcurrency int
				DiscoveryPaceMs      int
				MaxInflight          int
				MaxPathWindow        int
				MaxAnswer            int
				MaxQName             int
				MaxStreams           int
			}
		}
	}
	if e = json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
	if v.Schema != "whitedns.profile" || v.Version != 1 || v.Profile.Server.Domain != a.Zone || v.Profile.Server.PublicKey != a.PublicKey || !v.Profile.SoarSettings.Discovery || v.Profile.SoarSettings.Country != "IR" || len(v.Profile.Resolvers) == 0 || len(a.Zone) > 45 {
		t.Fatal("profile incompatible")
	}
	settings := v.Profile.SoarSettings
	if settings.Transport != "auto" || settings.DialTimeoutSeconds != 90 || settings.DiscoveryWant != 1 || settings.DiscoveryConcurrency != 1 || settings.DiscoveryPaceMs != 1500 {
		t.Fatal("export must retain paced fallback discovery without seeking eight healthy paths")
	}
	if settings.MaxInflight != 32 || settings.MaxPathWindow != 8 || settings.MaxAnswer != 512 || settings.MaxQName != 101 || settings.MaxStreams != 128 {
		t.Fatal("export must pin DNS packet budgets and allow full-device concurrent streams")
	}
	if strings.Contains(string(raw), "seed") || strings.Contains(string(raw), "private") {
		t.Fatal("private key leaked")
	}
	seed, _ := s.Seed(a.ID)
	otherSeed, _ := restored.Seed(a.ID)
	if !bytes.Equal(seed, otherSeed) {
		t.Fatal("backup lost private key")
	}
}
func TestPolicyAndOptimisticConcurrency(t *testing.T) {
	s := testStore(t)
	a := account(t, s, 100)
	in := input(a)
	in.Enabled = false
	if _, e := s.Edit(a.ID, in); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Edit(a.ID, in); !errors.Is(e, ErrConflict) {
		t.Fatal("stale edit overwrote policy")
	}
	if _, _, e := s.reserve(a.ID, 1, true); !errors.Is(e, ErrDenied) {
		t.Fatal("disabled accepted")
	}
	a, _ = s.Get(a.ID)
	in = input(a)
	in.Enabled = true
	in.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	if _, e := s.Edit(a.ID, in); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.reserve(a.ID, 1, false); !errors.Is(e, ErrDenied) {
		t.Fatal("expired accepted")
	}
	oldZone := a.Zone
	oldKey := a.PublicKey
	a, e := s.Rotate(a.ID)
	if e != nil || a.Zone == oldZone || a.PublicKey == oldKey {
		t.Fatal("rotation failed", e)
	}
	if e = s.Delete(a.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Seed(a.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("deleted seed survived")
	}
}
func TestSettingsLimits(t *testing.T) {
	cfg := Settings{BaseZone: "soar.example.org", Resolvers: []string{"1.1.1.1"}, AllowPorts: []uint16{443}}
	for _, base := range []string{"", "UPPER.example.org", "a..org", strings.Repeat("a", 25) + ".org", "-a.example.org"} {
		bad := cfg
		bad.BaseZone = base
		if bad.Validate() == nil {
			t.Error("bad zone accepted", base)
		}
	}
	bad := cfg
	bad.Resolvers = nil
	if bad.Validate() == nil {
		t.Fatal("empty bootstrap list accepted")
	}
}
