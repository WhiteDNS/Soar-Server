package panel

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func publicSubscription(t *testing.T, h *HTTP, path string, code int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", "http://example.test"+path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != code {
		t.Fatalf("subscription: want %d got %d", code, w.Code)
	}
	return w
}

func TestSubscriptionAuthenticationRotationAndFreshSettings(t *testing.T) {
	b, s, h := testBrowser(t)
	c := account(t, s, 100000)
	b.request("/api/clients/"+c.ID+"/subscription", "GET", nil, 401)
	b.login()
	var out struct {
		URL string `json:"url"`
	}
	json.Unmarshal(b.request("/api/clients/"+c.ID+"/subscription", "GET", nil, 200), &out)
	u, e := url.Parse(out.URL)
	if e != nil || !strings.HasPrefix(u.Path, "/sub/") {
		t.Fatal("missing subscription URL")
	}
	w := publicSubscription(t, h, u.Path, 200)
	body, e := base64.StdEncoding.DecodeString(strings.TrimSpace(w.Body.String()))
	want, _ := s.Profile(c.ID)
	if e != nil || string(body) != want+"\n" {
		t.Fatal("subscription profile differs from export")
	}
	if !strings.Contains(w.Header().Get("Subscription-Userinfo"), "total=100000") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing quota or privacy headers")
	}
	cfg, _ := s.Settings()
	cfg.Resolvers = []string{"9.9.9.9"}
	if e = s.SaveSettings(cfg); e != nil {
		t.Fatal(e)
	}
	w = publicSubscription(t, h, u.Path+"?format=plain", 200)
	want, _ = s.Profile(c.ID)
	if w.Body.String() != want+"\n" {
		t.Fatal("subscription cached obsolete settings")
	}
	oldKey := c.PublicKey
	rotated, e := s.Rotate(c.ID)
	if e != nil || rotated.PublicKey == oldKey {
		t.Fatal("account key rotation failed", e)
	}
	w = publicSubscription(t, h, u.Path+"?format=plain", 200)
	want, _ = s.Profile(c.ID)
	if w.Body.String() != want+"\n" {
		t.Fatal("same subscription URL did not return rotated tunnel key")
	}
	before, _ := s.Get(c.ID)
	json.Unmarshal(b.request("/api/clients/"+c.ID+"/subscription-rotate", "POST", nil, 200), &out)
	newURL, _ := url.Parse(out.URL)
	if newURL.Path == u.Path {
		t.Fatal("subscription URL did not rotate")
	}
	publicSubscription(t, h, u.Path, 404)
	publicSubscription(t, h, newURL.Path, 200)
	after, _ := s.Get(c.ID)
	if before != after {
		t.Fatal("subscription rotation changed account keys, policy or counters")
	}
	if e = s.Delete(c.ID); e != nil {
		t.Fatal(e)
	}
	publicSubscription(t, h, newURL.Path, 404)
}

func TestSubscriptionBackupRejectsBrokenIndex(t *testing.T) {
	for _, corrupt := range []string{"missing-index", "wrong-id", "short-token", "nested-token"} {
		t.Run(corrupt, func(t *testing.T) {
			s := testStore(t)
			c := account(t, s, 0)
			_, _ = s.SubscriptionToken(c.ID, false)
			if e := s.db.Update(func(tx *bolt.Tx) error {
				raw := append([]byte(nil), tx.Bucket(subscriptions).Get([]byte(c.ID))...)
				switch corrupt {
				case "missing-index":
					return tx.DeleteBucket(subscriptionIndex)
				case "wrong-id":
					return tx.Bucket(subscriptionIndex).Put(subscriptionDigest(raw), []byte("other-account"))
				case "short-token":
					return tx.Bucket(subscriptions).Put([]byte(c.ID), []byte("short"))
				case "nested-token":
					if e := tx.Bucket(subscriptions).Delete([]byte(c.ID)); e != nil {
						return e
					}
					_, e := tx.Bucket(subscriptions).CreateBucket([]byte(c.ID))
					return e
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(t.TempDir(), "corrupt.db")
			if e := writePrivate(path, backupBytes(t, s)); e != nil {
				t.Fatal(e)
			}
			if _, e := InspectBackupFile(path); e == nil {
				t.Fatal("corrupt subscription backup accepted")
			}
		})
	}
}

func TestSubscriptionInactiveAccountsNeverExportProfiles(t *testing.T) {
	_, s, h := testBrowser(t)
	c := account(t, s, 100)
	cred, _ := s.SubscriptionToken(c.ID, false)
	path := "/sub/" + cred
	for _, state := range []string{"disabled", "expired", "exhausted"} {
		e := s.db.Update(func(tx *bolt.Tx) error {
			c, _ := getClient(tx, c.ID)
			c.Enabled = state != "disabled"
			c.ExpiresAt = 0
			c.UploadBytes = 0
			if state == "expired" {
				c.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
			}
			if state == "exhausted" {
				c.UploadBytes = c.QuotaBytes
			}
			return putJSON(tx.Bucket(clients), []byte(c.ID), c)
		})
		if e != nil {
			t.Fatal(e)
		}
		w := publicSubscription(t, h, path, 403)
		if strings.Contains(w.Body.String(), "soar://") || w.Header().Get("X-WhiteDNS-Account-Status") != state {
			t.Fatal("inactive account exported a profile", state)
		}
	}
	for _, path := range []string{"/sub/short", "/sub/" + strings.Repeat("x", 43), "/sub/" + cred + "/extra"} {
		publicSubscription(t, h, path, 404)
	}
}

func TestSubscriptionDoesNotRevealPrivateAdminRoute(t *testing.T) {
	_, s, h := testBrowser(t)
	c := account(t, s, 0)
	cred, _ := s.SubscriptionToken(c.ID, false)
	h.access = &AccessController{cfg: AccessConfig{Domain: "panel.example.com", HTTPS: true, BasePath: "/abcdefghijklmnopqrstuvwxyz012345/"}}
	r := httptest.NewRequest("GET", "https://panel.example.com:443/sub/"+cred, nil)
	r.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), h.access.cfg.BasePath) {
		t.Fatal("public subscription depends on admin route")
	}
	if h.subscriptionURL(r, cred) != "https://panel.example.com/sub/"+cred {
		t.Fatal("subscription URL leaks admin path or noncanonical host")
	}
	r.Host = "wrong.example.com"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("subscription accepted unconfigured TLS host")
	}
}

func TestSubscriptionBackupAndLegacyCompatibility(t *testing.T) {
	s := testStore(t)
	c := account(t, s, 0)
	cred, e := s.SubscriptionToken(c.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "backup.db")
	if e = writePrivate(path, backupBytes(t, s)); e != nil {
		t.Fatal(e)
	}
	if _, e = InspectBackupFile(path); e != nil {
		t.Fatal("valid subscription backup rejected", e)
	}
	restored, e := OpenStore(path)
	if e != nil {
		t.Fatal(e)
	}
	x, _, e := restored.Subscription(cred)
	if e != nil || x.ID != c.ID {
		t.Fatal("restored subscription identity changed")
	}
	restored.Close()
	if e = s.db.Update(func(tx *bolt.Tx) error {
		if e := tx.DeleteBucket(subscriptions); e != nil {
			return e
		}
		return tx.DeleteBucket(subscriptionIndex)
	}); e != nil {
		t.Fatal(e)
	}
	legacy := filepath.Join(t.TempDir(), "legacy.db")
	if e = writePrivate(legacy, backupBytes(t, s)); e != nil {
		t.Fatal(e)
	}
	if _, e = InspectBackupFile(legacy); e != nil {
		t.Fatal("old backup rejected", e)
	}
	old, e := OpenStore(legacy)
	if e != nil {
		t.Fatal(e)
	}
	defer old.Close()
	if _, e = old.SubscriptionToken(c.ID, false); e != nil {
		t.Fatal("legacy migration failed", e)
	}
}
