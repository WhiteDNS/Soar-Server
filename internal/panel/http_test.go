package panel

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
)

type browser struct {
	t      *testing.T
	url    string
	client *http.Client
	csrf   string
}

func (b *browser) request(path, method string, body any, status int) []byte {
	b.t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r, e := http.NewRequest(method, b.url+path, bytes.NewReader(raw))
	if e != nil {
		b.t.Fatal(e)
	}
	if method != "GET" {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", b.csrf)
	}
	response, e := b.client.Do(r)
	if e != nil {
		b.t.Fatal(e)
	}
	defer response.Body.Close()
	value, e := io.ReadAll(response.Body)
	if e != nil {
		b.t.Fatal(e)
	}
	if response.StatusCode != status {
		b.t.Fatalf("%s %s: want %d got %d: %s", method, path, status, response.StatusCode, value)
	}
	return value
}
func testBrowser(t *testing.T) (*browser, *Store, *HTTP) {
	t.Helper()
	s := testStore(t)
	m := testManager(t, s, nil)
	h := NewHTTP(s, m, false)
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	b := &browser{t: t, url: server.URL, client: &http.Client{Jar: jar}}
	return b, s, h
}
func (b *browser) login() {
	b.t.Helper()
	raw := b.request("/api/login", "POST", map[string]string{"name": "admin", "password": "test-only-password-2026"}, 200)
	var v struct{ CSRF string }
	if e := json.Unmarshal(raw, &v); e != nil || v.CSRF == "" {
		b.t.Fatal("no CSRF token", e)
	}
	b.csrf = v.CSRF
}
func TestAuthenticationCSRFAndProfileEndpoints(t *testing.T) {
	b, s, _ := testBrowser(t)
	b.request("/api/backup", "GET", nil, 401)
	b.request("/api/clients", "GET", nil, 401)
	b.login()
	token := b.csrf
	b.csrf = ""
	b.request("/api/clients", "POST", ClientInput{Name: "Unsafe", Enabled: true, StreamLimit: 16}, 403)
	b.csrf = token
	raw := b.request("/api/clients", "POST", ClientInput{Name: "<img src=x onerror=alert(1)>", Enabled: true, QuotaBytes: 1024, StreamLimit: 16}, 201)
	var c Client
	if e := json.Unmarshal(raw, &c); e != nil {
		t.Fatal(e)
	}
	b.request("/api/clients/"+c.ID+"/link", "GET", nil, 200)
	png := b.request("/api/clients/"+c.ID+"/qr.png", "GET", nil, 200)
	if !bytes.HasPrefix(png, []byte{137, 80, 78, 71}) {
		t.Fatal("invalid QR image")
	}
	list := b.request("/api/clients", "GET", nil, 200)
	seed, _ := s.Seed(c.ID)
	if bytes.Contains(list, seed) || bytes.Contains(list, []byte("private_key")) {
		t.Fatal("private key exposed")
	}
	in := input(c)
	in.Enabled = false
	b.request("/api/clients/"+c.ID, "PUT", in, 200)
	b.request("/api/clients/"+c.ID, "PUT", in, 409)
	b.request("/api/clients/"+c.ID+"/reset", "POST", nil, 200)
	b.request("/api/clients/"+c.ID+"/rotate", "POST", nil, 200)
	b.request("/api/clients/"+c.ID, "DELETE", nil, 200)
	b.request("/api/clients/"+c.ID, "GET", nil, 404)
	b.request("/api/logout", "POST", nil, 200)
	b.request("/api/dashboard", "GET", nil, 401)
}
func TestOriginHeadersCookiesAndInputBounds(t *testing.T) {
	b, _, h := testBrowser(t)
	for _, origin := range []string{"https://attacker.example", "null"} {
		r, _ := http.NewRequest("POST", b.url+"/api/login", strings.NewReader(`{"name":"admin","password":"test-only-password-2026"}`))
		r.Header.Set("Origin", origin)
		response, e := b.client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatal("hostile login origin accepted")
		}
	}
	b.login()
	r, _ := http.NewRequest("DELETE", b.url+"/api/clients/"+strings.Repeat("a", 32), nil)
	r.Header.Set("X-CSRF-Token", b.csrf)
	r.Header.Set("Origin", "https://attacker.example")
	response, e := b.client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("cross origin write accepted")
	}
	response, e = b.client.Get(b.url + "/")
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if !strings.Contains(response.Header.Get("Content-Security-Policy"), "script-src 'self'") || response.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("security headers missing")
	}
	b.request("/api/clients", "POST", map[string]any{"unknown": "field"}, 400)
	req := httptest.NewRequest("GET", "http://panel/", nil)
	req.RemoteAddr = "203.0.113.2:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	h.trustProxy = true
	if h.isSecure(req) {
		t.Fatal("public forwarding header trusted")
	}
	req.RemoteAddr = "127.0.0.1:1234"
	if !h.isSecure(req) {
		t.Fatal("explicit loopback proxy not trusted")
	}
}
func TestBackupContainsRestorableDataAndAudit(t *testing.T) {
	b, s, _ := testBrowser(t)
	c := account(t, s, 1000)
	b.login()
	b.request("/api/clients", "GET", nil, 200)
	data := b.request("/api/backup", "GET", nil, 200)
	if len(data) < 4096 {
		t.Fatal("backup empty")
	}
	list, e := s.Events()
	if e != nil || len(list) < 2 || list[0].Action != "backup.exported" {
		t.Fatal("backup not audited", e)
	}
	dashboard := b.request("/api/dashboard", "GET", nil, 200)
	if !bytes.Contains(dashboard, []byte(`"accounts":1`)) {
		t.Fatal("dashboard has fake account count")
	}
	if _, e = s.Get(c.ID); e != nil {
		t.Fatal(e)
	}
}
func TestLoginThrottle(t *testing.T) {
	b, _, _ := testBrowser(t)
	for i := 0; i < 8; i++ {
		b.request("/api/login", "POST", map[string]string{"name": "admin", "password": "wrong"}, 401)
	}
	b.request("/api/login", "POST", map[string]string{"name": "admin", "password": "test-only-password-2026"}, 429)
}
