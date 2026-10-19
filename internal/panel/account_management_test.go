package panel

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
)

func TestAdminCredentialChangeRevokesEverySession(t *testing.T) {
	b, s, _ := testBrowser(t)
	b.request("/api/admin", "GET", nil, 401)
	b.request("/api/admin", "PUT", adminChange{}, 401)
	b.login()
	jar, _ := cookiejar.New(nil)
	other := &browser{t: t, url: b.url, client: &http.Client{Jar: jar}}
	other.login()
	raw := b.request("/api/admin", "GET", nil, 200)
	if string(bytes.TrimSpace(raw)) != `{"name":"admin"}` {
		t.Fatalf("unexpected administrator data: %s", raw)
	}
	in := adminChange{CurrentName: "admin", CurrentPassword: "incorrect-password", NewName: "new-admin", NewPassword: "new-test-only-password-2026"}
	csrf := b.csrf
	b.csrf = ""
	b.request("/api/admin", "PUT", in, 403)
	b.csrf = csrf
	b.request("/api/admin", "PUT", in, 403)
	b.request("/api/session", "GET", nil, 200) // Bad current password keeps the form accessible.
	in.CurrentPassword = "test-only-password-2026"
	in.NewPassword = "short"
	b.request("/api/admin", "PUT", in, 400)
	in.NewPassword = "new-test-only-password-2026"
	b.request("/api/admin", "PUT", in, 200)
	b.request("/api/session", "GET", nil, 401)
	other.request("/api/session", "GET", nil, 401)
	b.request("/api/login", "POST", map[string]string{"name": "admin", "password": "test-only-password-2026"}, 401)
	if !s.CheckAdmin("new-admin", in.NewPassword) || s.CheckAdmin("new-admin", in.CurrentPassword) {
		t.Fatal("credentials were not replaced")
	}
	b.request("/api/login", "POST", map[string]string{"name": "new-admin", "password": in.NewPassword}, 200)
	if raw := b.request("/api/events", "GET", nil, 200); bytes.Contains(raw, []byte(in.NewPassword)) || bytes.Contains(raw, []byte(in.CurrentPassword)) || !bytes.Contains(raw, []byte("admin.credentials_updated")) {
		t.Fatal("credential audit event missing or contains a password")
	}
}

func TestAdminUsernameOnlyAndPasswordOnly(t *testing.T) {
	s := testStore(t)
	in := adminChange{CurrentName: "admin", CurrentPassword: "test-only-password-2026", NewName: "renamed"}
	if err := s.changeAdmin(in); err != nil || !s.CheckAdmin("renamed", in.CurrentPassword) {
		t.Fatal("username-only change lost password", err)
	}
	in.CurrentName, in.NewName, in.NewPassword = "renamed", "renamed", "another-test-only-password"
	if err := s.changeAdmin(in); err != nil || !s.CheckAdmin("renamed", in.NewPassword) {
		t.Fatal("password-only change failed", err)
	}
	in.CurrentPassword = in.NewPassword
	for _, badName := range []string{"", "spaces invalid", "<script>", strings.Repeat("a", 65)} {
		in.NewName = badName
		if err := s.changeAdmin(in); err == nil {
			t.Fatal("invalid name accepted", badName)
		}
	}
	in.NewName = "renamed"
	for _, badPassword := range []string{"too-short", strings.Repeat("a", 73), "new-password-\n-hidden", "new-password-فارسی"} {
		in.NewPassword = badPassword
		if err := s.changeAdmin(in); err == nil {
			t.Fatal("invalid password accepted")
		}
	}
}

func TestAdminCredentialChangesCannotOverwriteEachOther(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"first-admin", "second-admin"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			results <- s.changeAdmin(adminChange{CurrentName: "admin", CurrentPassword: "test-only-password-2026", NewName: name, NewPassword: "new-test-only-password-2026"})
		}(name)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, errAdminChanged) && !errors.Is(err, errAdminCredentials) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("%d concurrent credential changes committed", success)
	}
}

func TestAdminCurrentPasswordIsRateLimited(t *testing.T) {
	b, _, _ := testBrowser(t)
	b.login()
	for i := 0; i < 8; i++ {
		b.request("/api/admin", "PUT", adminChange{CurrentName: "admin", CurrentPassword: "wrong-password", NewName: "admin"}, 403)
	}
	b.request("/api/admin", "PUT", adminChange{CurrentName: "admin", CurrentPassword: "test-only-password-2026", NewName: "admin"}, 429)
}

func TestBulkDeleteIsAtomicAndRevokesKeysSubscriptionsAndRuntime(t *testing.T) {
	b, s, h := testBrowser(t)
	a, c, keep := account(t, s, 0), account(t, s, 0), account(t, s, 0)
	sub, err := s.SubscriptionToken(a.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.manager.Reconcile(); err != nil {
		t.Fatal(err)
	}
	ids := map[string]any{"ids": []string{a.ID, c.ID}}
	b.request("/api/clients/delete", "POST", ids, 401)
	b.login()
	csrf := b.csrf
	b.csrf = ""
	b.request("/api/clients/delete", "POST", ids, 403)
	b.csrf = csrf
	for _, bad := range [][]string{{}, {a.ID, a.ID}, {a.ID, "invalid"}, {a.ID, strings.Repeat("f", 32)}} {
		status := 400
		if len(bad) == 2 && len(bad[1]) == 32 && bad[1] != a.ID {
			status = 404
		}
		b.request("/api/clients/delete", "POST", map[string]any{"ids": bad}, status)
		if _, err := s.Get(a.ID); err != nil {
			t.Fatal("invalid batch partially deleted a valid account")
		}
	}
	b.request("/api/clients/delete", "GET", nil, 405)
	before, _ := s.Events()
	raw := b.request("/api/clients/delete", "POST", ids, 200)
	if !bytes.Contains(raw, []byte(`"deleted":2`)) {
		t.Fatal(string(raw))
	}
	for _, id := range []string{a.ID, c.ID} {
		if _, err := s.Get(id); !errors.Is(err, ErrNotFound) {
			t.Fatal("account survived deletion", err)
		}
		if _, err := s.Seed(id); err == nil {
			t.Fatal("account key survived deletion")
		}
		if _, err := s.Profile(id); err == nil {
			t.Fatal("deleted profile can still be exported")
		}
	}
	if _, _, err := s.Subscription(sub); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted subscription still resolves", err)
	}
	if _, err := s.Get(keep.ID); err != nil {
		t.Fatal("unselected account deleted", err)
	}
	h.manager.mu.RLock()
	_, aLive := h.manager.accounts[a.ID]
	_, cLive := h.manager.accounts[c.ID]
	_, keepLive := h.manager.accounts[keep.ID]
	h.manager.mu.RUnlock()
	if aLive || cLive || !keepLive {
		t.Fatal("runtime deletion did not match selection")
	}
	after, _ := s.Events()
	if len(after) != len(before)+2 {
		t.Fatal("missing per-account deletion events")
	}
}

func TestBulkDeleteAcceptsMaximumSelection(t *testing.T) {
	b, s, _ := testBrowser(t)
	ids := make([]string, MaxClients)
	for i := range ids {
		ids[i] = account(t, s, 0).ID
	}
	b.login()
	oversized := append(append([]string{}, ids...), strings.Repeat("f", 32))
	b.request("/api/clients/delete", "POST", map[string]any{"ids": oversized}, 400)
	body := map[string]any{"ids": ids}
	raw, _ := json.Marshal(body)
	if len(raw) <= 16*1024 {
		t.Fatal("test must cover a request larger than the standard body limit")
	}
	b.request("/api/clients/delete", "POST", body, 200)
	list, err := s.List()
	if err != nil || len(list) != 0 {
		t.Fatal("maximum selection not deleted", err)
	}
}
