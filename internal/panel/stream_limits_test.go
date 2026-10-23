package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestStreamLimitsValidationAndSettingsCompatibility(t *testing.T) {
	for _, limit := range []int{0, 1, 129, MaxStreamLimit} {
		if err := (ClientInput{Name: "limit", Enabled: true, StreamLimit: limit}).validate(); err != nil {
			t.Fatalf("valid account stream limit %d: %v", limit, err)
		}
	}
	for _, limit := range []int{-1, MaxStreamLimit + 1} {
		if err := (ClientInput{Name: "limit", Enabled: true, StreamLimit: limit}).validate(); err == nil {
			t.Fatal("invalid account limit accepted", limit)
		}
	}
	b, s, h := testBrowser(t)
	b.login()
	cfg, _ := s.Settings()
	cfg.ServerStreamLimit = 7
	b.request("/api/settings", "PUT", cfg, 200)
	var legacy map[string]any
	raw, _ := json.Marshal(cfg)
	_ = json.Unmarshal(raw, &legacy)
	delete(legacy, "server_stream_limit")
	b.request("/api/settings", "PUT", legacy, 200)
	saved, _ := s.Settings()
	if saved.ServerStreamLimit != 7 {
		t.Fatal("old settings API request erased explicit server limit")
	}
	for _, limit := range []int{0, MaxStreamLimit} {
		cfg.ServerStreamLimit = limit
		b.request("/api/settings", "PUT", cfg, 200)
	}
	for _, limit := range []int{-1, MaxStreamLimit + 1} {
		cfg.ServerStreamLimit = limit
		b.request("/api/settings", "PUT", cfg, 400)
	}
	if got, _ := s.Settings(); got.ServerStreamLimit != MaxStreamLimit {
		t.Fatal("invalid settings changed saved global limit")
	}
	var c Client
	if err := json.Unmarshal(b.request("/api/clients", "POST", ClientInput{Name: "unlimited", Enabled: true}, 201), &c); err != nil || c.StreamLimit != 0 {
		t.Fatal("new account did not default to unlimited", err)
	}
	h.manager.mu.RLock()
	oldRuntime := h.manager.accounts[c.ID]
	h.manager.mu.RUnlock()
	in := input(c)
	in.StreamLimit = 400
	b.request("/api/clients/"+c.ID, "PUT", in, 200)
	h.manager.mu.RLock()
	current := h.manager.accounts[c.ID]
	h.manager.mu.RUnlock()
	if oldRuntime == current || oldRuntime.ctx.Err() == nil || current.client.StreamLimit != 400 {
		t.Fatal("account stream change did not rebuild its fixed engine caps")
	}
	c, _ = s.Get(c.ID)
	in = input(c)
	in.Note = "metadata edit"
	b.request("/api/clients/"+c.ID, "PUT", in, 200)
	h.manager.mu.RLock()
	unchanged := h.manager.accounts[c.ID]
	h.manager.mu.RUnlock()
	if current != unchanged {
		t.Fatal("metadata-only update restarted the runtime")
	}
}

func TestStreamLimitsConcurrentReservationsAndDynamicGlobalLimit(t *testing.T) {
	for _, scenario := range []struct {
		name                            string
		accountLimit, globalLimit, want int
	}{
		{name: "account", accountLimit: 3, want: 3},
		{name: "server", globalLimit: 5, want: 5},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s := testStore(t)
			a, err := s.Create(ClientInput{Name: "first", Enabled: true, StreamLimit: scenario.accountLimit})
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.Create(ClientInput{Name: "second", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			cfg, _ := s.Settings()
			cfg.ServerStreamLimit = scenario.globalLimit
			if err = s.SaveSettings(cfg); err != nil {
				t.Fatal(err)
			}
			release := make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			started := make(chan struct{}, 40)
			m := testManager(t, s, func(ctx context.Context, _, _ string) (net.Conn, error) {
				started <- struct{}{}
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				local, peer := net.Pipe()
				go func() { defer peer.Close(); _, _ = io.Copy(io.Discard, peer) }()
				return local, nil
			})
			m.mu.RLock()
			first, second := m.accounts[a.ID], m.accounts[b.ID]
			m.mu.RUnlock()
			results := make(chan net.Conn, 40)
			var wg sync.WaitGroup
			for n := range 40 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					rt := first
					if scenario.name == "server" && n%2 == 1 {
						rt = second
					}
					conn, _ := m.dial(rt, context.Background(), "tcp", "example.com:443")
					results <- conn
				}()
			}
			for range scenario.want {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("expected pending reservation did not reach dialer")
				}
			}
			if got := m.active.Load(); got != uint64(scenario.want) {
				t.Fatalf("pending dials bypassed cap: %d", got)
			}
			once.Do(func() { close(release) })
			wg.Wait()
			close(results)
			var conns []net.Conn
			for conn := range results {
				if conn != nil {
					conns = append(conns, conn)
				}
			}
			if len(conns) != scenario.want {
				t.Fatalf("concurrent admissions = %d, want %d", len(conns), scenario.want)
			}
			if scenario.name == "server" {
				cfg.ServerStreamLimit = 1
				if err = s.SaveSettings(cfg); err != nil {
					t.Fatal(err)
				}
				if err = m.Reconcile(); err != nil {
					t.Fatal(err)
				}
				m.mu.RLock()
				if m.accounts[a.ID] != first || m.accounts[b.ID] != second {
					t.Error("global limit update restarted accounts")
				}
				m.mu.RUnlock()
				if conn, e := m.dial(first, context.Background(), "tcp", "example.com:443"); e == nil {
					conn.Close()
					t.Fatal("lower global limit admitted another stream")
				}
				if m.active.Load() != uint64(scenario.want) {
					t.Fatal("lower global limit killed existing streams")
				}
				cfg.ServerStreamLimit = 0
				if err = s.SaveSettings(cfg); err != nil {
					t.Fatal(err)
				}
				if err = m.Reconcile(); err != nil {
					t.Fatal(err)
				}
				conn, e := m.dial(first, context.Background(), "tcp", "example.com:443")
				if e != nil {
					t.Fatal("unlimited global setting did not apply immediately", e)
				}
				conns = append(conns, conn)
			}
			for _, conn := range conns {
				wg.Add(1)
				go func() { defer wg.Done(); _ = conn.Close(); _ = conn.Close() }()
			}
			wg.Wait()
			if m.active.Load() != 0 {
				t.Fatal("close leaked or double-refunded capacity")
			}
			m.dialOverride = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("dial failed") }
			for range 5 {
				if _, e := m.dial(first, context.Background(), "tcp", "example.com:443"); e == nil {
					t.Fatal("failed dial accepted")
				}
			}
			first.mu.Lock()
			pending := first.pending
			first.mu.Unlock()
			if pending != 0 || m.active.Load() != 0 {
				t.Fatal("failed dials leaked reservations")
			}
		})
	}
}

func TestStreamLimitsNativeSoarAbove128AcrossSessions(t *testing.T) {
	s := testStore(t)
	c, err := s.Create(ClientInput{Name: "many streams", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	m := testManager(t, s, func(context.Context, string, string) (net.Conn, error) {
		local, peer := net.Pipe()
		go func() { defer peer.Close(); _, _ = io.Copy(peer, peer) }()
		return local, nil
	})
	for range 3 {
		client := nativeClient(t, m, c)
		for range 45 {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			conn, err := client.DialContext(ctx, "tcp", "example.com:443")
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close() })
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err = conn.Write([]byte("x")); err != nil {
				t.Fatal(err)
			}
			var response [1]byte
			if _, err = io.ReadFull(conn, response[:]); err != nil || response[0] != 'x' {
				t.Fatal("stream above prior aggregate cap failed", err)
			}
		}
	}
	if m.active.Load() != 135 {
		t.Fatalf("native active streams = %d, want 135", m.active.Load())
	}
}

func logicalStreamMigrationRows(t *testing.T, db *bolt.DB) map[string]map[string][]byte {
	t.Helper()
	out := map[string]map[string][]byte{}
	if err := db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, bucket *bolt.Bucket) error {
			rows := map[string][]byte{}
			out[string(name)] = rows
			return bucket.ForEach(func(key, value []byte) error { rows[string(key)] = bytes.Clone(value); return nil })
		})
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func replaceTestRawField(t *testing.T, s *Store, bucket []byte, key, field string, value json.RawMessage) {
	t.Helper()
	if err := s.db.Update(func(tx *bolt.Tx) error {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(tx.Bucket(bucket).Get([]byte(key)), &fields); err != nil {
			return err
		}
		setRawLimit(fields, field, value)
		return putJSON(tx.Bucket(bucket), []byte(key), fields)
	}); err != nil {
		t.Fatal(err)
	}
}

func expectedRawField(t *testing.T, rows map[string]map[string][]byte, bucket, key, field string, value json.RawMessage) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rows[bucket][key], &fields); err != nil {
		t.Fatal(err)
	}
	setRawLimit(fields, field, value)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	rows[bucket][key] = raw
}

func TestStreamLimitsOfflineMigrationAndRollbackPreserveAllOtherData(t *testing.T) {
	s := testStore(t)
	a, b := account(t, s, 123456), account(t, s, 0)
	if _, _, err := s.reserve(a.ID, 99, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubscriptionToken(a.ID, false); err != nil {
		t.Fatal(err)
	}
	replaceTestRawField(t, s, clients, a.ID, "future_account", json.RawMessage(`{"opaque":[1,"unchanged"]}`))
	replaceTestRawField(t, s, meta, "settings", "future_setting", json.RawMessage(`{"keep":true}`))
	// An older settings document has no global-cap field at all.
	replaceTestRawField(t, s, meta, "settings", "server_stream_limit", nil)
	before := logicalStreamMigrationRows(t, s.db)
	path := s.db.Path()
	var backup bytes.Buffer
	if err := s.Backup(&backup); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "before.db")
	if err := os.WriteFile(backupPath, backup.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := MigrateStreamLimits(path, "", nil)
	if err != nil || result.Accounts != 2 || result.Restored {
		t.Fatal("migration failed", result, err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	for key := range before["clients"] {
		expectedRawField(t, before, "clients", key, "stream_limit", json.RawMessage("0"))
	}
	expectedRawField(t, before, "meta", "settings", "server_stream_limit", json.RawMessage("0"))
	if !reflect.DeepEqual(before, logicalStreamMigrationRows(t, s.db)) {
		t.Fatal("migration changed data beyond requested stream fields")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = MigrateStreamLimits(path, "", nil); err != nil {
		t.Fatal("second migration failed", err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, logicalStreamMigrationRows(t, s.db)) {
		t.Fatal("migration is not idempotent")
	}
	// Simulate activity and user changes after deployment, then roll back only
	// stream fields. A full database restore would incorrectly erase this usage.
	if _, _, err = s.reserve(a.ID, 71, false); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(b.ID); err != nil {
		t.Fatal(err)
	}
	newAccount, err := s.Create(ClientInput{Name: "created after deployment", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	replaceTestRawField(t, s, clients, a.ID, "future_account", json.RawMessage(`{"newer":true}`))
	replaceTestRawField(t, s, clients, newAccount.ID, "future_account", json.RawMessage(`"new account preserved"`))
	live := logicalStreamMigrationRows(t, s.db)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	result, err = MigrateStreamLimits(path, backupPath, sessionLimitPointer(128))
	if err != nil || !result.Restored || result.Accounts != 2 {
		t.Fatal("rollback failed", result, err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	expectedRawField(t, live, "clients", a.ID, "stream_limit", json.RawMessage("16"))
	expectedRawField(t, live, "clients", newAccount.ID, "stream_limit", json.RawMessage("128"))
	expectedRawField(t, live, "meta", "settings", "server_stream_limit", nil)
	if !reflect.DeepEqual(live, logicalStreamMigrationRows(t, s.db)) {
		t.Fatal("rollback changed usage, keys, accounts, unknown fields or metadata")
	}
	if c, err := s.Get(a.ID); err != nil || c.UploadBytes != 99 || c.DownloadBytes != 71 {
		t.Fatal("rollback lost post-deployment usage", err)
	}
	if _, err = s.Get(b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("rollback recreated deleted account")
	}
}

func TestStreamLimitsOfflineMigrationRefusesLiveOrInvalidDatabase(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	path := s.db.Path()
	if _, err := MigrateStreamLimits(path, "", nil); err == nil {
		t.Fatal("migration accepted database held by running panel")
	}
	replaceTestRawField(t, s, clients, c.ID, "stream_limit", json.RawMessage("-1"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if _, err := MigrateStreamLimits(path, "", nil); err == nil {
		t.Fatal("migration accepted invalid existing account")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed validation wrote database changes")
	}
	if _, err := MigrateStreamLimits(filepath.Join(t.TempDir(), "absent.db"), "", nil); err == nil {
		t.Fatal("migration created a missing database")
	}
}
