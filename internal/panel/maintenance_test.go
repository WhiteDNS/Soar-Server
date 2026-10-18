package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	bolt "go.etcd.io/bbolt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeMaintenance struct{ requests []MaintenanceRequest }

func (f *fakeMaintenance) Status(context.Context) (MaintenanceJob, error) {
	return MaintenanceJob{State: "idle"}, nil
}
func (f *fakeMaintenance) Start(_ context.Context, r MaintenanceRequest) (MaintenanceJob, error) {
	f.requests = append(f.requests, r)
	return MaintenanceJob{ID: "test-job", Kind: r.Action, State: "running"}, nil
}
func (f *fakeMaintenance) AllowHTTPS(context.Context) error { return nil }
func backupBytes(t *testing.T, s *Store) []byte {
	t.Helper()
	var b bytes.Buffer
	if e := s.Backup(&b); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func uploadBackup(t *testing.T, b *browser, raw []byte, status int) []byte {
	t.Helper()
	r, _ := http.NewRequest("POST", b.url+"/api/restore/preview", bytes.NewReader(raw))
	r.Header.Set("X-CSRF-Token", b.csrf)
	r.Header.Set("Content-Type", "application/octet-stream")
	response, e := b.client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	out, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("backup preview: want %d got %d: %s", status, response.StatusCode, out)
	}
	return out
}
func TestRestoreValidationReauthenticationAndSingleUse(t *testing.T) {
	b, s, h := testBrowser(t)
	fake := &fakeMaintenance{}
	h.SetMaintenance(fake, nil)
	b.request("/api/maintenance", "GET", nil, 401)
	uploadBackup(t, b, []byte("not a database"), 401)
	b.login()
	client, e := s.Create(ClientInput{Name: "Saved account", Enabled: true, StreamLimit: 8, QuotaBytes: 12345})
	if e != nil {
		t.Fatal(e)
	}
	backup := backupBytes(t, s)
	uploadBackup(t, b, []byte("not a database"), 400)
	token := b.csrf
	b.csrf = "bad-token"
	uploadBackup(t, b, backup, 403)
	b.csrf = token
	var preview struct {
		Token   string
		Summary BackupSummary
	}
	if e = json.Unmarshal(uploadBackup(t, b, backup, 200), &preview); e != nil {
		t.Fatal(e)
	}
	if preview.Summary.Accounts != 1 || preview.Token == "" {
		t.Fatal("missing restore preview")
	}
	b.request("/api/restore/apply", "POST", map[string]string{"token": preview.Token, "password": "wrong"}, 403)
	if len(fake.requests) != 0 {
		t.Fatal("wrong password started restore")
	}
	b.request("/api/restore/apply", "POST", map[string]string{"token": preview.Token, "password": "test-only-password-2026"}, 202)
	if len(fake.requests) != 1 || !bytes.Equal(fake.requests[0].Backup, backup) || fake.requests[0].ExpectedZone != preview.Summary.BaseZone {
		t.Fatal("backup changed between preview and apply")
	}
	b.request("/api/restore/apply", "POST", map[string]string{"token": preview.Token, "password": "test-only-password-2026"}, 400)
	if _, e = s.Get(client.ID); e != nil {
		t.Fatal("preview altered live accounts")
	}
	if e = json.Unmarshal(uploadBackup(t, b, backup, 200), &preview); e != nil {
		t.Fatal(e)
	}
	h.mu.Lock()
	h.preview.Expires = time.Now().Add(-time.Minute)
	h.mu.Unlock()
	b.request("/api/restore/apply", "POST", map[string]string{"token": preview.Token, "password": "test-only-password-2026"}, 400)
}
func TestOfficialUpdateSelectionAndPassword(t *testing.T) {
	b, _, h := testBrowser(t)
	fake := &fakeMaintenance{}
	h.SetMaintenance(fake, nil)
	b.login()
	for _, v := range []string{"0.1.3", "0.2.0-beta7", "https://attacker.test/a", "0.2.0-beta11;id"} {
		b.request("/api/updates/install", "POST", map[string]string{"version": v, "password": "test-only-password-2026"}, 400)
	}
	b.request("/api/updates/install", "POST", map[string]string{"version": "0.2.0-beta11", "password": "wrong"}, 403)
	b.request("/api/updates/install", "POST", map[string]string{"version": "0.2.0-beta11", "password": "test-only-password-2026"}, 202)
	if len(fake.requests) != 1 || fake.requests[0].Version != "0.2.0-beta11" {
		t.Fatal("unexpected update request")
	}
}
func TestBackupSemanticValidationAndAdminPreservation(t *testing.T) {
	current := testStore(t)
	backup := testStore(t)
	c, e := backup.Create(ClientInput{Name: "Restored", Enabled: true, StreamLimit: 16})
	if e != nil {
		t.Fatal(e)
	}
	if e = backup.ResetAdmin("other", "different-password-2026"); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "restored.db")
	if e = os.WriteFile(path, backupBytes(t, backup), 0600); e != nil {
		t.Fatal(e)
	}
	summary, e := InspectBackupFile(path)
	if e != nil || summary.Accounts != 1 {
		t.Fatal(summary, e)
	}
	currentPath := current.db.Path()
	_ = current.Close()
	if e = PreserveRestoreAdmin(path, currentPath); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(path)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	if !restored.CheckAdmin("admin", "test-only-password-2026") || restored.CheckAdmin("other", "different-password-2026") {
		t.Fatal("restore replaced current admin")
	}
	if _, e = restored.Get(c.ID); e != nil {
		t.Fatal(e)
	}
	if e = backup.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(seeds).Put([]byte(c.ID), make([]byte, 32)) }); e != nil {
		t.Fatal(e)
	}
	bad := filepath.Join(t.TempDir(), "bad.db")
	_ = os.WriteFile(bad, backupBytes(t, backup), 0600)
	if _, e = InspectBackupFile(bad); e == nil {
		t.Fatal("mismatched private key accepted")
	}
}
func TestRandomPathPreservesOldAccessUntilApply(t *testing.T) {
	b, _, h := testBrowser(t)
	a, e := NewAccessController(filepath.Join(t.TempDir(), "access.json"))
	if e != nil {
		t.Fatal(e)
	}
	h.SetAccess(a)
	b.login()
	var plan AccessPlan
	raw := b.request("/api/access/prepare", "POST", AccessInput{GeneratePath: true, Password: "test-only-password-2026"}, 202)
	if e = json.Unmarshal(raw, &plan); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 100 && a.Plan().State == "preparing"; i++ {
		time.Sleep(time.Millisecond)
	}
	if a.Plan().State != "ready" {
		t.Fatal(a.Plan())
	}
	b.request("/api/session", "GET", nil, 200)
	b.request("/api/access/apply", "POST", map[string]string{"id": plan.ID}, 200)
	b.request("/api/session", "GET", nil, 404)
	b.request("/app.js", "GET", nil, 404)
	prefix := a.Config().BasePath
	if !securePathPattern.MatchString(prefix) {
		t.Fatal("weak random path")
	}
	b.url += strings.TrimSuffix(prefix, "/")
	b.request("/api/session", "GET", nil, 401)
	b.login()
	html := string(b.request("/", "GET", nil, 200))
	if !strings.Contains(html, `<base href="`+prefix+`">`) {
		t.Fatal("subpath base missing")
	}
	b.request("/app.js", "GET", nil, 200)
	b.request("/fonts/vazirmatn.woff2", "GET", nil, 200)
	b.request("/api/clients", "GET", nil, 200)
}
func TestHTTPSFailureLeavesOldConfigAndHostGate(t *testing.T) {
	a, e := NewAccessController(filepath.Join(t.TempDir(), "access.json"))
	if e != nil {
		t.Fatal(e)
	}
	a.lookup = func(context.Context, string) ([]string, error) { return []string{"203.0.113.1"}, nil }
	a.issue = func(string) error { return errors.New("fixture certificate failure") }
	_, e = a.Prepare("http://127.0.0.1:2053", "owner", AccessInput{Domain: "panel.example.org", HTTPS: true, GeneratePath: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 100 && a.Plan().State == "preparing"; i++ {
		time.Sleep(time.Millisecond)
	}
	if a.Plan().State != "failed" || a.Config().HTTPS || a.Config().BasePath != "" {
		t.Fatal("failed certificate changed current access")
	}
	if _, e = a.Apply(a.Plan().ID, "owner"); e == nil {
		t.Fatal("failed plan applied")
	}
	a.issue = func(string) error { return nil }
	_, e = a.Prepare("http://127.0.0.1:2053", "owner", AccessInput{Domain: "panel.example.org", HTTPS: true, GeneratePath: true}, nil)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 100 && a.Plan().State == "preparing"; i++ {
		time.Sleep(time.Millisecond)
	}
	if _, e = a.Apply(a.Plan().ID, "wrong-owner"); e == nil {
		t.Fatal("plan was not session-bound")
	}
	if _, e = a.Apply(a.Plan().ID, "owner"); e != nil {
		t.Fatal(e)
	}
	b, _, h := testBrowser(t)
	h.SetAccess(a)
	r := httptest.NewRequest("GET", "http://panel.example.org"+a.Config().BasePath, nil)
	r.RemoteAddr = "203.0.113.9:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("plaintext login remains exposed")
	}
	b.request("/api/session", "GET", nil, 404)
}
func TestVersionOrdering(t *testing.T) {
	for _, v := range [][2]string{{"0.2.0-beta10", "0.2.0-beta9"}, {"0.2.0", "0.2.0-beta99"}, {"0.3.0-beta1", "0.2.0"}} {
		if !NewerVersion(v[0], v[1]) || NewerVersion(v[1], v[0]) {
			t.Fatal(v)
		}
	}
	if ValidVersion("0.2.0-beta1/../") || NewerVersion("garbage", "0.2.0") {
		t.Fatal("unsafe version accepted")
	}
}
