package panel

import (
	"crypto/hmac"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

//go:embed web/* web/fonts/*
var webFiles embed.FS

type loginSession struct {
	csrf    string
	expires time.Time
}
type loginAttempt struct {
	count int
	until time.Time
}
type HTTP struct {
	store      *Store
	manager    *Manager
	trustProxy bool
	mu         sync.Mutex
	sessions   map[string]loginSession
	attempts   map[string]loginAttempt
	assets     http.Handler
	metrics    hostSampler
}

func NewHTTP(s *Store, m *Manager, trustLoopbackProxy bool) *HTTP {
	assets, _ := fs.Sub(webFiles, "web")
	return &HTTP{store: s, manager: m, trustProxy: trustLoopbackProxy, sessions: map[string]loginSession{}, attempts: map[string]loginAttempt{}, assets: http.FileServer(http.FS(assets))}
}
func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func errorReply(w http.ResponseWriter, status int, e error) {
	jsonReply(w, status, map[string]string{"error": e.Error()})
}
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if e := dec.Decode(v); e != nil {
		return errors.New("invalid JSON request")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("unexpected request data")
	}
	return nil
}
func token() string {
	raw := make([]byte, 32)
	if _, e := rand.Read(raw); e != nil {
		panic("secure random unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
func (h *HTTP) isSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !h.trustProxy {
		return false
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-Proto") == "https"
}
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, e := url.Parse(origin)
	return e == nil && u.Host == r.Host && (u.Scheme == "https" || u.Scheme == "http")
}
func (h *HTTP) authenticated(r *http.Request) (loginSession, bool) {
	cookie, e := r.Cookie("soar_panel_session")
	if e != nil {
		return loginSession{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[cookie.Value]
	if !ok || time.Now().After(s.expires) {
		delete(h.sessions, cookie.Value)
		return loginSession{}, false
	}
	return s, true
}
func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; font-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Cache-Control", "no-store")
	if h.isSecure(r) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
	if r.URL.Path == "/api/login" && r.Method == "POST" {
		h.login(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		switch r.URL.Path {
		case "/", "/index.html", "/app.js", "/settings.js", "/style.css", "/fonts/vazirmatn.ttf":
			h.assets.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
		return
	}
	session, ok := h.authenticated(r)
	if !ok {
		errorReply(w, 401, errors.New("sign in required"))
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		if !sameOrigin(r) || !hmac.Equal([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.csrf)) {
			errorReply(w, 403, errors.New("invalid request origin or CSRF token"))
			return
		}
	}
	if r.URL.Path == "/api/session" && r.Method == "GET" {
		jsonReply(w, 200, map[string]any{"csrf": session.csrf, "expires_at": session.expires.UnixMilli()})
		return
	}
	if r.URL.Path == "/api/logout" && r.Method == "POST" {
		c, _ := r.Cookie("soar_panel_session")
		h.mu.Lock()
		delete(h.sessions, c.Value)
		h.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "soar_panel_session", Value: "", Path: "/", HttpOnly: true, Secure: h.isSecure(r), SameSite: http.SameSiteStrictMode, MaxAge: -1})
		jsonReply(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.URL.Path == "/api/dashboard" && r.Method == "GET" {
		h.dashboard(w)
		return
	}
	if r.URL.Path == "/api/events" && r.Method == "GET" {
		v, e := h.store.Events()
		if e != nil {
			errorReply(w, 500, errors.New("cannot read audit events"))
			return
		}
		jsonReply(w, 200, v)
		return
	}
	if r.URL.Path == "/api/backup" && r.Method == "GET" {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="soar-panel-backup.db"`)
		_ = h.store.Audit("backup.exported", "")
		_ = h.store.Backup(w)
		return
	}
	if r.URL.Path == "/api/settings" {
		h.settings(w, r)
		return
	}
	if r.URL.Path == "/api/clients" {
		h.clientList(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/clients/") {
		h.clientAction(w, r)
		return
	}
	http.NotFound(w, r)
}
func (h *HTTP) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		errorReply(w, 403, errors.New("invalid request origin"))
		return
	}
	var in struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if e := decodeJSON(w, r, &in); e != nil {
		errorReply(w, 400, e)
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	now := time.Now()
	h.mu.Lock()
	a := h.attempts[ip]
	if now.After(a.until) {
		a = loginAttempt{until: now.Add(5 * time.Minute)}
	}
	if a.count >= 8 || len(h.attempts) >= 1024 && h.attempts[ip].until.IsZero() {
		h.mu.Unlock()
		errorReply(w, 429, errors.New("too many login attempts; retry after five minutes"))
		return
	}
	a.count++
	h.attempts[ip] = a
	for key, v := range h.attempts {
		if now.After(v.until) {
			delete(h.attempts, key)
		}
	}
	h.mu.Unlock()
	if len(in.Password) > 72 || !h.store.CheckAdmin(in.Name, in.Password) {
		errorReply(w, 401, errors.New("invalid username or password"))
		return
	}
	value := token()
	s := loginSession{csrf: token(), expires: now.Add(12 * time.Hour)}
	h.mu.Lock()
	delete(h.attempts, ip)
	for key, v := range h.sessions {
		if now.After(v.expires) {
			delete(h.sessions, key)
		}
	}
	if len(h.sessions) >= 128 {
		h.mu.Unlock()
		errorReply(w, 429, errors.New("too many admin sessions"))
		return
	}
	h.sessions[value] = s
	h.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "soar_panel_session", Value: value, Path: "/", HttpOnly: true, Secure: h.isSecure(r), SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	_ = h.store.Audit("admin.login", "")
	jsonReply(w, 200, map[string]string{"csrf": s.csrf})
}
func (h *HTTP) dashboard(w http.ResponseWriter) {
	list, e := h.store.List()
	if e != nil {
		errorReply(w, 500, errors.New("cannot read accounts"))
		return
	}
	states := map[string]int{"active": 0, "disabled": 0, "expired": 0, "exhausted": 0}
	var up, down int64
	for _, c := range list {
		states[c.Status(time.Now())]++
		up += c.UploadBytes
		down += c.DownloadBytes
	}
	counts := h.manager.ActiveCounts()
	online := 0
	connected := 0
	for _, n := range counts {
		connected += n
		if n > 0 {
			online++
		}
	}
	jsonReply(w, 200, map[string]any{"accounts": len(list), "states": states, "upload_bytes": up, "download_bytes": down, "active_streams": connected, "accounts_with_streams": online, "dns_received_bytes": h.manager.dnsIn.Load(), "dns_sent_bytes": h.manager.dnsOut.Load(), "uptime_seconds": int64(time.Since(h.manager.started).Seconds()), "host": h.metrics.sample(), "version": "0.2.0-beta4"})
}
func (h *HTTP) settings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		v, e := h.store.Settings()
		if e != nil {
			errorReply(w, 500, errors.New("cannot read settings"))
			return
		}
		jsonReply(w, 200, v)
		return
	}
	if r.Method != "PUT" {
		w.WriteHeader(405)
		return
	}
	var in Settings
	if e := decodeJSON(w, r, &in); e != nil {
		errorReply(w, 400, e)
		return
	}
	if e := h.store.SaveSettings(in); e != nil {
		errorReply(w, 400, e)
		return
	}
	if e := h.manager.Reconcile(); e != nil {
		errorReply(w, 500, errors.New("saved settings; runtime synchronization failed"))
		return
	}
	jsonReply(w, 200, in)
}
func (h *HTTP) clientList(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		list, e := h.store.List()
		if e != nil {
			errorReply(w, 500, errors.New("cannot read accounts"))
			return
		}
		sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt > list[j].CreatedAt })
		jsonReply(w, 200, map[string]any{"clients": list, "connections": h.manager.ActiveCounts(), "now": time.Now().UnixMilli()})
	case "POST":
		var in ClientInput
		if e := decodeJSON(w, r, &in); e != nil {
			errorReply(w, 400, e)
			return
		}
		c, e := h.store.Create(in)
		if e != nil {
			errorReply(w, 400, e)
			return
		}
		if e := h.manager.Reconcile(); e != nil {
			errorReply(w, 500, errors.New("account saved; runtime synchronization failed"))
			return
		}
		jsonReply(w, 201, c)
	default:
		w.WriteHeader(405)
	}
}
func (h *HTTP) clientAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/clients/"), "/")
	id := parts[0]
	if len(id) != 32 || len(parts) > 2 {
		http.NotFound(w, r)
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	c, e := h.store.Get(id)
	if e != nil {
		errorReply(w, 404, ErrNotFound)
		return
	}
	if r.Method == "GET" {
		if action == "" {
			jsonReply(w, 200, c)
			return
		}
		if action == "link" || action == "qr.png" {
			link, e := h.store.Profile(id)
			if e != nil {
				errorReply(w, 500, errors.New("cannot render profile"))
				return
			}
			if action == "link" {
				jsonReply(w, 200, map[string]string{"link": link})
				return
			}
			png, e := qrcode.Encode(link, qrcode.Medium, 512)
			if e != nil {
				errorReply(w, 500, errors.New("cannot render QR"))
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Write(png)
			return
		}
		http.NotFound(w, r)
		return
	}
	switch {
	case r.Method == "PUT" && action == "":
		var in ClientInput
		if e = decodeJSON(w, r, &in); e == nil {
			c, e = h.store.Edit(id, in)
		}
	case r.Method == "DELETE" && action == "":
		e = h.store.Delete(id)
	case r.Method == "POST" && action == "reset":
		h.manager.Disconnect(id)
		e = h.store.ResetUsage(id)
	case r.Method == "POST" && action == "rotate":
		c, e = h.store.Rotate(id)
	case r.Method == "POST" && action == "disconnect":
		h.manager.Disconnect(id)
		e = h.store.Audit("client.disconnected", id)
	default:
		w.WriteHeader(405)
		return
	}
	if e != nil {
		status := 400
		if errors.Is(e, ErrConflict) {
			status = 409
		}
		errorReply(w, status, e)
		return
	}
	if e = h.manager.Reconcile(); e != nil {
		errorReply(w, 500, errors.New("saved change; runtime synchronization failed"))
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}
