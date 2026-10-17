package panel

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func exportedSettings(t *testing.T, link string) ProfileSettings {
	t.Helper()
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "soar://"))
	if e != nil {
		t.Fatal(e)
	}
	var doc struct {
		Profile struct{ SoarSettings ProfileSettings }
	}
	if e := json.Unmarshal(raw, &doc); e != nil {
		t.Fatal(e)
	}
	return doc.Profile.SoarSettings
}

func TestProfileTuningPersistsExportsAndKeepsAccountIdentity(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 12345)
	_, _, _ = s.reserve(c.ID, 123, true)
	oldLink, _ := s.Profile(c.ID)
	cfg, _ := s.Settings()
	p := DefaultProfileSettings()
	p.Transport, p.Discovery = "only", false
	p.SafeQName, p.MaxQName, p.MaxAnswer = 128, 180, 896
	p.MaxInflight, p.MaxPathWindow, p.HoldMs = 64, 16, 200
	p.DiscoveryWant, p.DiscoveryConcurrency, p.DiscoveryPaceMs = 4, 2, 2500
	p.MaxStreams, p.StreamWindowKiB, p.SendBufferKiB = 96, 512, 128
	p.SessionIdleSeconds, p.DialTimeoutSeconds = 60, 120
	cfg.SoarSettings = &p
	if e := s.SaveSettings(cfg); e != nil {
		t.Fatal(e)
	}
	// A legacy settings caller must not reset the newly saved tuning.
	cfg.SoarSettings = nil
	cfg.AllowPorts = []uint16{443}
	if e := s.SaveSettings(cfg); e != nil {
		t.Fatal(e)
	}
	link, e := s.Profile(c.ID)
	if e != nil || exportedSettings(t, link) != p {
		t.Fatal("saved settings not exported", e)
	}
	if exportedSettings(t, oldLink) != DefaultProfileSettings() {
		t.Fatal("old link changed")
	}
	actual, _ := s.Get(c.ID)
	if actual.Zone != c.Zone || actual.PublicKey != c.PublicKey || actual.QuotaBytes != 12345 || actual.UploadBytes != 123 {
		t.Fatal("tuning changed identity, budget or usage")
	}
	// Reopen the database to verify actual persistence rather than an in-memory copy.
	backup := filepath.Join(t.TempDir(), "restore.db")
	if e := s.db.View(func(tx *bolt.Tx) error { return tx.CopyFile(backup, 0600) }); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(backup)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	saved, _ := restored.Settings()
	if saved.SoarSettings == nil || *saved.SoarSettings != p {
		t.Fatal("tuning lost on backup/restore")
	}
}

func TestLegacySettingsUseBeta3Defaults(t *testing.T) {
	s := testStore(t) // Init writes the pre-tuning schema with no soarSettings.
	cfg, e := s.Settings()
	if e != nil || cfg.SoarSettings == nil || *cfg.SoarSettings != DefaultProfileSettings() {
		t.Fatal("legacy settings changed behavior", e)
	}
}

func TestProfileTuningValidationAndAtomicRejection(t *testing.T) {
	s := testStore(t)
	cfg, _ := s.Settings()
	for _, tc := range []struct {
		name string
		bad  func(*ProfileSettings)
	}{
		{"transport", func(p *ProfileSettings) { p.Transport = "tls" }},
		{"want", func(p *ProfileSettings) { p.DiscoveryWant = 0 }},
		{"concurrency", func(p *ProfileSettings) { p.DiscoveryConcurrency = 17 }},
		{"pace", func(p *ProfileSettings) { p.DiscoveryPaceMs = 0 }},
		{"safe", func(p *ProfileSettings) { p.SafeQName = 100 }},
		{"crossed-lengths", func(p *ProfileSettings) { p.SafeQName = 128; p.MaxQName = 101 }},
		{"name", func(p *ProfileSettings) { p.MaxQName = 254 }},
		{"answer", func(p *ProfileSettings) { p.MaxAnswer = 4096 }},
		{"hold", func(p *ProfileSettings) { p.HoldMs = 0 }},
		{"inflight", func(p *ProfileSettings) { p.MaxInflight = 2048 }},
		{"path-window", func(p *ProfileSettings) { p.MaxPathWindow = 1 }},
		{"server-stream-cap", func(p *ProfileSettings) { p.MaxStreams = 129 }},
		{"receive-window", func(p *ProfileSettings) { p.StreamWindowKiB = 32 }},
		{"send-buffer", func(p *ProfileSettings) { p.SendBufferKiB = 2048 }},
		{"server-idle", func(p *ProfileSettings) { p.SessionIdleSeconds = 120 }},
		{"timeout", func(p *ProfileSettings) { p.DialTimeoutSeconds = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultProfileSettings()
			tc.bad(&p)
			bad := cfg
			bad.SoarSettings = &p
			if e := s.SaveSettings(bad); e == nil {
				t.Fatal("invalid tuning accepted")
			}
			after, _ := s.Settings()
			if !reflect.DeepEqual(after, cfg) {
				t.Fatal("rejected save changed stored settings")
			}
		})
	}
}

func TestSettingsAPIValidatesTuningAndExportsIt(t *testing.T) {
	b, s, _ := testBrowser(t)
	c := account(t, s, 0)
	b.login()
	var cfg Settings
	if e := json.Unmarshal(b.request("/api/settings", "GET", nil, 200), &cfg); e != nil {
		t.Fatal(e)
	}
	cfg.SoarSettings.MaxQName, cfg.SoarSettings.MaxAnswer = 180, 896
	b.request("/api/settings", "PUT", cfg, 200)
	var share struct{ Link string }
	_ = json.Unmarshal(b.request("/api/clients/"+c.ID+"/link", "GET", nil, 200), &share)
	if exportedSettings(t, share.Link) != *cfg.SoarSettings {
		t.Fatal("API export differs from settings")
	}
	cfg.SoarSettings.MaxStreams = 256 // Supported by the library, not this server.
	b.request("/api/settings", "PUT", cfg, 400)
	b.request("/api/settings", "PUT", map[string]any{"soarSettings": map[string]any{"encryption": "none"}}, 400)
	saved, _ := s.Settings()
	if saved.SoarSettings.MaxStreams != 128 || saved.SoarSettings.MaxQName != 180 {
		t.Fatal("rejected HTTP save mutated settings")
	}
}
