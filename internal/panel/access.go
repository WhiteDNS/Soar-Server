package panel

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

type AccessConfig struct {
	Aliases  []string `json:"aliases,omitempty"`
	BasePath string   `json:"base_path"`
	Domain   string   `json:"domain"`
	HTTPS    bool     `json:"https"`
	Email    string   `json:"email,omitempty"`
}
type AccessInput struct {
	Aliases      []string `json:"aliases"`
	Domain       string   `json:"domain"`
	HTTPS        bool     `json:"https"`
	Email        string   `json:"email"`
	GeneratePath bool     `json:"generate_path"`
	BasePath     string   `json:"base_path"`
	Password     string   `json:"password"`
}
type AccessPlan struct {
	ID      string       `json:"id"`
	State   string       `json:"state"`
	URL     string       `json:"url"`
	Error   string       `json:"error,omitempty"`
	Config  AccessConfig `json:"config"`
	Expires int64        `json:"expires"`
	owner   string
}
type AccessController struct {
	mu                      sync.RWMutex
	tlsMu                   sync.Mutex
	file                    string
	cfg                     AccessConfig
	plan                    *AccessPlan
	requestedHost           string
	requestedHosts          map[string]bool
	handler                 http.Handler
	manager                 *autocert.Manager
	servers                 []*http.Server
	listeners               []net.Listener
	localIPs                map[string]bool
	httpAddress, tlsAddress string
	// Tests replace only certificate issuance; production always uses ACME.
	issue  func(string) error
	lookup func(context.Context, string) ([]string, error)
}

var securePathPattern = regexp.MustCompile(`^/[A-Za-z0-9_-]{24,96}/$`)

func ValidateDomain(domain string) error {
	if len(domain) > 253 || !strings.Contains(domain, ".") || net.ParseIP(domain) != nil {
		return errors.New("enter a full domain such as panel.example.com")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("invalid panel domain")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return errors.New("panel domain must contain ASCII letters, numbers or hyphens")
			}
		}
	}
	return nil
}
func ValidateAccess(c AccessConfig) error {
	if len(c.Aliases) > 7 {
		return errors.New("use at most eight panel domains")
	}
	seen := map[string]bool{c.Domain: true}
	for _, domain := range c.Aliases {
		if !c.HTTPS || ValidateDomain(domain) != nil || seen[domain] {
			return errors.New("panel aliases must be unique HTTPS domains")
		}
		seen[domain] = true
	}
	if c.BasePath != "" && !securePathPattern.MatchString(c.BasePath) {
		return errors.New("secure path must contain 24–96 random URL-safe characters")
	}
	if c.HTTPS {
		if e := ValidateDomain(c.Domain); e != nil {
			return e
		}
	} else if c.Domain != "" {
		return errors.New("a domain requires HTTPS")
	}
	if len(c.Email) > 254 || strings.ContainsAny(c.Email, "\r\n\x00") {
		return errors.New("invalid certificate email")
	}
	return nil
}
func ReadAccessConfig(file string) (c AccessConfig, e error) {
	raw, e := os.ReadFile(file)
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	if len(raw) > 4096 {
		return c, errors.New("access configuration is too large")
	}
	if e = json.Unmarshal(raw, &c); e == nil {
		e = ValidateAccess(c)
	}
	return
}
func WritePrivateJSON(path string, value any) error {
	raw, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".config-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	_ = f.Chmod(0600)
	if _, e = f.Write(raw); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e == nil {
		e = os.Rename(name, path)
	}
	return e
}
func NewAccessController(file string) (*AccessController, error) {
	cfg, e := ReadAccessConfig(file)
	if e != nil {
		return nil, e
	}
	a := &AccessController{file: file, cfg: cfg, httpAddress: ":80", tlsAddress: ":443", localIPs: map[string]bool{"127.0.0.1": true, "::1": true}}
	if addresses, e := net.InterfaceAddrs(); e == nil {
		for _, address := range addresses {
			host, _, _ := strings.Cut(address.String(), "/")
			a.localIPs[host] = true
		}
	}
	return a, nil
}
func (c AccessConfig) domains() []string {
	if !c.HTTPS {
		return nil
	}
	return append([]string{c.Domain}, c.Aliases...)
}
func (c AccessConfig) hasDomain(host string) bool {
	for _, domain := range c.domains() {
		if strings.EqualFold(host, domain) {
			return true
		}
	}
	return false
}
func (a *AccessController) Config() AccessConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	cfg := a.cfg
	cfg.Aliases = append([]string(nil), cfg.Aliases...)
	return cfg
}
func (a *AccessController) localPeer(remote string) bool {
	host, _, _ := net.SplitHostPort(remote)
	return a.localIPs[host]
}
func (a *AccessController) Start(handler http.Handler) error {
	a.handler = handler
	if a.Config().HTTPS {
		return a.startTLS()
	}
	return nil
}
func (a *AccessController) Close() {
	a.tlsMu.Lock()
	defer a.tlsMu.Unlock()
	for _, s := range a.servers {
		_ = s.Close()
	}
	for _, l := range a.listeners {
		_ = l.Close()
	}
	a.servers = nil
	a.listeners = nil
}
func (a *AccessController) startTLS() error {
	a.tlsMu.Lock()
	defer a.tlsMu.Unlock()
	if a.manager != nil {
		return nil
	}
	httpListener, e := net.Listen("tcp", a.httpAddress)
	if e != nil {
		return fmt.Errorf("HTTP certificate challenge port 80 unavailable: %w", e)
	}
	tlsListener, e := net.Listen("tcp", a.tlsAddress)
	if e != nil {
		httpListener.Close()
		return fmt.Errorf("HTTPS port 443 unavailable: %w", e)
	}
	cfg := a.Config()
	a.mu.RLock()
	if a.plan != nil {
		cfg = a.plan.Config
	}
	a.mu.RUnlock()
	manager := &autocert.Manager{Prompt: autocert.AcceptTOS, Email: cfg.Email, Cache: autocert.DirCache(filepath.Join(filepath.Dir(a.file), "certificates")), HostPolicy: func(ctx context.Context, host string) error {
		a.mu.RLock()
		defer a.mu.RUnlock()
		if a.cfg.hasDomain(host) || a.requestedHosts[host] && a.plan != nil && time.Now().UnixMilli() < a.plan.Expires {
			return nil
		}
		return errors.New("unconfigured certificate domain")
	}}
	tlsConfig := manager.TLSConfig()
	tlsConfig.MinVersion = tls.VersionTLS12
	https := &http.Server{Handler: a.handler, TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	challenges := &http.Server{Handler: manager.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := a.Config()
		host := strings.Split(r.Host, ":")[0]
		if cfg.hasDomain(host) && (cfg.BasePath == "" || strings.HasPrefix(r.URL.Path, cfg.BasePath)) {
			http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusPermanentRedirect)
			return
		}
		http.NotFound(w, r)
	})), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
	a.manager = manager
	a.servers = []*http.Server{https, challenges}
	a.listeners = []net.Listener{tlsListener, httpListener}
	go func() { _ = https.ServeTLS(tlsListener, "", "") }()
	go func() { _ = challenges.Serve(httpListener) }()
	return nil
}
func (a *AccessController) Plan() *AccessPlan {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.plan == nil {
		return nil
	}
	p := *a.plan
	return &p
}
func (a *AccessController) Prepare(origin, owner string, in AccessInput, firewall func(context.Context) error) (*AccessPlan, error) {
	c := AccessConfig{Domain: strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Domain), ".")), HTTPS: in.HTTPS, Email: strings.TrimSpace(in.Email), BasePath: in.BasePath}
	for _, domain := range in.Aliases {
		c.Aliases = append(c.Aliases, strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), ".")))
	}
	if in.Aliases == nil && c.HTTPS {
		previous := a.Config()
		for _, domain := range previous.domains() {
			if domain != c.Domain {
				c.Aliases = append(c.Aliases, domain)
			}
		}
	}
	if in.GeneratePath {
		c.BasePath = "/" + token() + "/"
	}
	if e := ValidateAccess(c); e != nil {
		return nil, e
	}
	if a.Config().HTTPS && !c.HTTPS {
		return nil, errors.New("use the terminal recovery command to disable HTTPS")
	}
	if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
		return nil, errors.New("invalid current panel address")
	}
	next := origin + c.BasePath
	if c.BasePath == "" {
		next += "/"
	}
	if c.HTTPS {
		next = "https://" + c.Domain + c.BasePath
		if c.BasePath == "" {
			next += "/"
		}
	}
	a.mu.Lock()
	if a.plan != nil && a.plan.State == "preparing" {
		a.mu.Unlock()
		return nil, errors.New("certificate preparation is already running")
	}
	p := &AccessPlan{ID: token(), State: "preparing", URL: next, Config: c, Expires: time.Now().Add(15 * time.Minute).UnixMilli(), owner: owner}
	a.plan = p
	a.requestedHost = c.Domain
	a.requestedHosts = map[string]bool{}
	for _, domain := range c.domains() {
		a.requestedHosts[domain] = true
	}
	a.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		var e error
		if c.HTTPS {
			lookup := a.lookup
			if lookup == nil {
				lookup = net.DefaultResolver.LookupHost
			}
			for _, domain := range c.domains() {
				if _, e = lookup(ctx, domain); e != nil {
					e = errors.New("domain DNS lookup failed; create its A record first")
					break
				}
			}
			if e == nil && firewall != nil {
				e = firewall(ctx)
			}
			if e == nil {
				for _, domain := range c.domains() {
					if a.issue != nil {
						e = a.issue(domain)
					} else if e = a.startTLS(); e == nil {
						cert, ce := a.manager.GetCertificate(&tls.ClientHelloInfo{ServerName: domain})
						e = ce
						if e == nil && cert == nil {
							e = errors.New("certificate was not issued")
						}
					}
					if e != nil {
						break
					}
				}
			}
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.plan != p {
			return
		}
		if e != nil {
			p.State = "failed"
			p.Error = "Certificate setup failed. Check the domain A/AAAA records and inbound TCP ports 80/443; the previous address is unchanged. " + e.Error()
			a.requestedHost = ""
			a.requestedHosts = nil
		} else {
			p.State = "ready"
		}
	}()
	return a.Plan(), nil
}
func (a *AccessController) Apply(id, owner string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.plan
	if p == nil || p.ID != id || p.owner != owner || p.State != "ready" || time.Now().UnixMilli() > p.Expires {
		return "", errors.New("access preview expired or is not ready")
	}
	if e := WritePrivateJSON(a.file, p.Config); e != nil {
		return "", e
	}
	a.cfg = p.Config
	a.requestedHost = ""
	a.requestedHosts = nil
	a.plan = nil
	return p.URL, nil
}
func (h *HTTP) SetAccess(a *AccessController) { h.access = a }
func (h *HTTP) cookiePath() string {
	if h.access != nil {
		if p := h.access.Config().BasePath; p != "" {
			return p
		}
	}
	return "/"
}
func (h *HTTP) accessAPI(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/access") {
		return false
	}
	if h.access == nil {
		errorReply(w, 503, errors.New("access settings unavailable in this installation"))
		return true
	}
	if r.URL.Path == "/api/access" && r.Method == "GET" {
		jsonReply(w, 200, map[string]any{"config": h.access.Config(), "plan": h.access.Plan()})
		return true
	}
	if r.URL.Path == "/api/access/prepare" && r.Method == "POST" {
		var in AccessInput
		if e := decodeJSON(w, r, &in); e != nil {
			errorReply(w, 400, e)
			return true
		}
		if !h.maintenancePassword(w, r, in.Password) {
			return true
		}
		session, _ := h.authenticated(r)
		scheme := "http"
		if h.isSecure(r) {
			scheme = "https"
		}
		var firewall func(context.Context) error
		if h.ops != nil {
			firewall = h.ops.AllowHTTPS
		}
		plan, e := h.access.Prepare(scheme+"://"+r.Host, session.csrf, in, firewall)
		if e != nil {
			errorReply(w, 400, e)
		} else {
			jsonReply(w, 202, plan)
		}
		return true
	}
	if r.URL.Path == "/api/access/apply" && r.Method == "POST" {
		var in struct {
			ID string `json:"id"`
		}
		if e := decodeJSON(w, r, &in); e != nil {
			errorReply(w, 400, e)
			return true
		}
		session, _ := h.authenticated(r)
		next, e := h.access.Apply(in.ID, session.csrf)
		if e != nil {
			errorReply(w, 400, e)
			return true
		}
		_ = h.store.Audit("access.updated", "")
		h.mu.Lock()
		clear(h.sessions)
		h.mu.Unlock()
		jsonReply(w, 200, map[string]string{"url": next})
		return true
	}
	w.WriteHeader(405)
	return true
}
