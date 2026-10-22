package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func sessionLimitPointer(n int) *int { return &n }

func clientRecord(t *testing.T, s *Store, id string) []byte {
	t.Helper()
	var raw []byte
	if err := s.db.View(func(tx *bolt.Tx) error {
		raw = bytes.Clone(tx.Bucket(clients).Get([]byte(id)))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSessionLimitsLegacyReadsAndBackupPreserveAccount(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 12345)
	if _, _, err := s.reserve(c.ID, 99, true); err != nil {
		t.Fatal(err)
	}
	c, _ = s.Get(c.ID)
	seed, _ := s.Seed(c.ID)
	settings, _ := s.Settings()
	links, _ := s.Profiles(c.ID)
	var legacy map[string]any
	if err := json.Unmarshal(clientRecord(t, s, c.ID), &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "soar_session_limit")
	legacy["cotten_session_limit"] = 0
	if err := s.db.Update(func(tx *bolt.Tx) error { return putJSON(tx.Bucket(clients), []byte(c.ID), legacy) }); err != nil {
		t.Fatal(err)
	}
	before := clientRecord(t, s, c.ID)
	got, err := s.Get(c.ID)
	if err != nil || got != c || got.SoarSessionLimit != MaxSoarSessionLimit || got.CottenSessionLimit != MaxCottenSessionLimit {
		t.Fatalf("legacy defaults changed account: got=%+v err=%v", got, err)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0] != c || !bytes.Equal(before, clientRecord(t, s, c.ID)) {
		t.Fatal("reading legacy limits changed stored data", err)
	}
	var backup bytes.Buffer
	if err = s.Backup(&backup); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "legacy.db")
	if err = os.WriteFile(path, backup.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = InspectBackupFile(path); err != nil {
		t.Fatal("legacy backup rejected", err)
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(backup.Bytes(), unchanged) {
		t.Fatal("backup inspection rewrote the database")
	}
	restored, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err = restored.Get(c.ID)
	afterSeed, _ := restored.Seed(c.ID)
	afterSettings, _ := restored.Settings()
	afterLinks, _ := restored.Profiles(c.ID)
	if err != nil || got != c || !bytes.Equal(seed, afterSeed) || !reflect.DeepEqual(settings, afterSettings) || !reflect.DeepEqual(links, afterLinks) {
		t.Fatal("legacy backup lost identity, metadata, usage, settings or profile", err)
	}
}

func TestSessionLimitsValidationAndOmittedEdits(t *testing.T) {
	base := ClientInput{Name: "limits", Enabled: true, StreamLimit: 16}
	for _, limits := range []struct{ soar, cotten int }{{1, 1}, {MaxSoarSessionLimit, MaxCottenSessionLimit}} {
		in := base
		in.SoarSessionLimit, in.CottenSessionLimit = sessionLimitPointer(limits.soar), sessionLimitPointer(limits.cotten)
		if err := in.validate(); err != nil {
			t.Fatal("valid session limits rejected", err)
		}
	}
	for _, field := range []string{"soar", "cotten"} {
		maximum := MaxSoarSessionLimit
		if field == "cotten" {
			maximum = MaxCottenSessionLimit
		}
		for _, n := range []int{-1, 0, maximum + 1} {
			in := base
			if field == "soar" {
				in.SoarSessionLimit = sessionLimitPointer(n)
			} else {
				in.CottenSessionLimit = sessionLimitPointer(n)
			}
			if in.validate() == nil {
				t.Fatalf("accepted invalid %s session limit %d", field, n)
			}
		}
	}
	s := testStore(t)
	base.SoarSessionLimit, base.CottenSessionLimit = sessionLimitPointer(13), sessionLimitPointer(21)
	c, err := s.Create(base)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := s.Seed(c.ID)
	if _, _, err = s.reserve(c.ID, 71, false); err != nil {
		t.Fatal(err)
	}
	c, _ = s.Get(c.ID)
	in := input(c) // Old API clients omit both limits.
	in.Name = "metadata changed"
	updated, err := s.Edit(c.ID, in)
	if err != nil || updated.SoarSessionLimit != 13 || updated.CottenSessionLimit != 21 {
		t.Fatal("omitted limit reset saved values", err)
	}
	in = input(updated)
	in.SoarSessionLimit = sessionLimitPointer(17)
	updated, err = s.Edit(c.ID, in)
	afterSeed, _ := s.Seed(c.ID)
	if err != nil || updated.SoarSessionLimit != 17 || updated.CottenSessionLimit != 21 || updated.Used() != c.Used() || updated.UsageEpoch != c.UsageEpoch || updated.PublicKey != c.PublicKey || updated.Zone != c.Zone || updated.CreatedAt != c.CreatedAt || !bytes.Equal(seed, afterSeed) {
		t.Fatal("limit update changed other account state", err)
	}
	var backup bytes.Buffer
	if err = s.Backup(&backup); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "custom.db")
	if err = os.WriteFile(path, backup.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = InspectBackupFile(path); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.Get(c.ID)
	if err != nil || got != updated {
		t.Fatal("backup lost custom session limits", err)
	}
	if err = restored.db.Update(func(tx *bolt.Tx) error {
		got.CottenSessionLimit = MaxCottenSessionLimit + 1
		return putJSON(tx.Bucket(clients), []byte(got.ID), got)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.Get(c.ID); err == nil {
		t.Fatal("invalid stored limit silently normalized")
	}
	if err = restored.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = InspectBackupFile(path); err == nil {
		t.Fatal("invalid backup session limit accepted")
	}
}

func TestSessionLimitsAuthenticatedAPI(t *testing.T) {
	b, s, _ := testBrowser(t)
	in := ClientInput{Name: "API limits", Enabled: true, StreamLimit: 16, SoarSessionLimit: sessionLimitPointer(11), CottenSessionLimit: sessionLimitPointer(12)}
	b.request("/api/clients", "POST", in, 401)
	b.login()
	csrf := b.csrf
	b.csrf = ""
	b.request("/api/clients", "POST", in, 403)
	b.csrf = csrf
	var c Client
	if err := json.Unmarshal(b.request("/api/clients", "POST", in, 201), &c); err != nil || c.SoarSessionLimit != 11 || c.CottenSessionLimit != 12 {
		t.Fatal("API did not persist custom limits", err)
	}
	in = input(c)
	in.SoarSessionLimit = sessionLimitPointer(0)
	b.request("/api/clients/"+c.ID, "PUT", in, 400)
	got, _ := s.Get(c.ID)
	if got != c {
		t.Fatal("rejected API update changed the account")
	}
	var defaults Client
	if err := json.Unmarshal(b.request("/api/clients", "POST", ClientInput{Name: "default limits", Enabled: true, StreamLimit: 16}, 201), &defaults); err != nil || defaults.SoarSessionLimit != MaxSoarSessionLimit || defaults.CottenSessionLimit != MaxCottenSessionLimit {
		t.Fatal("API defaults are not maximum session limits", err)
	}
	list := b.request("/api/clients", "GET", nil, 200)
	if !bytes.Contains(list, []byte(`"soar_session_limit":11`)) || !bytes.Contains(list, []byte(`"cotten_session_limit":12`)) {
		t.Fatal("account metadata omits session limits")
	}
}

func TestSessionLimitsReconcileOnlyAffectedAccount(t *testing.T) {
	s := testStore(t)
	a, b := account(t, s, 0), account(t, s, 0)
	m := testManager(t, s, nil)
	runtimeFor := func(id string) *accountRuntime {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return m.accounts[id]
	}
	other := runtimeFor(b.ID)
	for _, field := range []string{"soar", "cotten"} {
		before := runtimeFor(a.ID)
		a, _ = s.Get(a.ID)
		in := input(a)
		if field == "soar" {
			in.SoarSessionLimit = sessionLimitPointer(3)
		} else {
			in.CottenSessionLimit = sessionLimitPointer(5)
		}
		if _, err := s.Edit(a.ID, in); err != nil {
			t.Fatal(err)
		}
		if err := m.Reconcile(); err != nil {
			t.Fatal(err)
		}
		current := runtimeFor(a.ID)
		if current == before || before.ctx.Err() == nil || runtimeFor(b.ID) != other || other.ctx.Err() != nil {
			t.Fatal("session-limit change did not selectively replace the affected account")
		}
		a, _ = s.Get(a.ID)
		in = input(a)
		in.SoarSessionLimit, in.CottenSessionLimit = sessionLimitPointer(a.SoarSessionLimit), sessionLimitPointer(a.CottenSessionLimit)
		in.Note = "metadata-only edit"
		if _, err := s.Edit(a.ID, in); err != nil {
			t.Fatal(err)
		}
		if err := m.Reconcile(); err != nil || runtimeFor(a.ID) != current || runtimeFor(b.ID) != other {
			t.Fatal("unchanged limits or metadata edit restarted account", err)
		}
	}
}

func TestSoarSessionLimitsAppliedToNativeClients(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	m := testManager(t, s, func(context.Context, string, string) (net.Conn, error) {
		local, peer := net.Pipe()
		go func() { defer peer.Close(); _, _ = io.Copy(peer, peer) }()
		return local, nil
	})
	connect := func() {
		t.Helper()
		client := nativeClient(t, m, c)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, err := client.DialContext(ctx, "tcp", "example.com:443")
		if err != nil {
			t.Fatal("native session failed", err)
		}
		t.Cleanup(func() { conn.Close() })
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err = conn.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		var echo [1]byte
		if _, err = io.ReadFull(conn, echo[:]); err != nil || echo[0] != 'x' {
			t.Fatal("native stream was not accepted", err)
		}
	}
	// Existing default configurations used to refuse the ninth independent device.
	for range 9 {
		connect()
	}
	c, _ = s.Get(c.ID)
	in := input(c)
	in.SoarSessionLimit = sessionLimitPointer(2)
	if _, err := s.Edit(c.ID, in); err != nil {
		t.Fatal(err)
	}
	if err := m.Reconcile(); err != nil {
		t.Fatal(err)
	}
	connect()
	connect()
	extra := nativeClient(t, m, c)
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	if conn, err := extra.DialContext(ctx, "tcp", "example.com:443"); err == nil {
		conn.Close()
		t.Fatal("configured two-session limit did not reject a third native client")
	}
}
