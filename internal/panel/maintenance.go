package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type MaintenanceJob struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Message  string `json:"message"`
	Version  string `json:"version,omitempty"`
	Backup   string `json:"backup,omitempty"`
	Started  int64  `json:"started"`
	Finished int64  `json:"finished,omitempty"`
}
type MaintenanceRequest struct {
	Action       string `json:"action"`
	Version      string `json:"version,omitempty"`
	Backup       []byte `json:"backup,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	ExpectedZone string `json:"expected_zone,omitempty"`
}
type MaintenanceOps interface {
	Status(context.Context) (MaintenanceJob, error)
	Start(context.Context, MaintenanceRequest) (MaintenanceJob, error)
	AllowHTTPS(context.Context) error
}
type OpsClient struct{ client *http.Client }

func NewOpsClient(socket string) *OpsClient {
	return &OpsClient{&http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}}
}
func (c *OpsClient) call(ctx context.Context, path string, body any, out any) error {
	var raw []byte
	method := "GET"
	if body != nil {
		method = "POST"
		raw, _ = json.Marshal(body)
	}
	req, e := http.NewRequestWithContext(ctx, method, "http://ops"+path, bytes.NewReader(raw))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	r, e := c.client.Do(req)
	if e != nil {
		return errors.New("maintenance service unavailable; rerun the current panel installer")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&v)
		if v.Error == "" {
			v.Error = "maintenance request rejected"
		}
		return errors.New(v.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(out)
}
func (c *OpsClient) Status(ctx context.Context) (v MaintenanceJob, e error) {
	e = c.call(ctx, "/status", nil, &v)
	return
}
func (c *OpsClient) Start(ctx context.Context, r MaintenanceRequest) (v MaintenanceJob, e error) {
	e = c.call(ctx, "/start", r, &v)
	return
}
func (c *OpsClient) AllowHTTPS(ctx context.Context) error {
	return c.call(ctx, "/firewall", struct{}{}, nil)
}

type restorePreview struct {
	Token, CSRF string
	Data        []byte
	Summary     BackupSummary
	Expires     time.Time
}

func (h *HTTP) SetMaintenance(ops MaintenanceOps, inspect func(context.Context, string) (BackupSummary, error)) {
	h.ops = ops
	if inspect != nil {
		h.inspectBackup = inspect
	}
}
func (h *HTTP) maintenancePassword(w http.ResponseWriter, r *http.Request, password string) bool {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	key := "reauth:" + ip
	now := time.Now()
	h.mu.Lock()
	a := h.attempts[key]
	if now.After(a.until) {
		a = loginAttempt{until: now.Add(5 * time.Minute)}
	}
	if a.count >= 8 {
		h.mu.Unlock()
		errorReply(w, 429, errors.New("too many password checks; retry after five minutes"))
		return false
	}
	a.count++
	h.attempts[key] = a
	h.mu.Unlock()
	if !h.store.VerifyPassword(password) {
		errorReply(w, 403, errors.New("current administrator password is incorrect"))
		return false
	}
	h.mu.Lock()
	delete(h.attempts, key)
	h.mu.Unlock()
	return true
}
func (h *HTTP) maintenance(w http.ResponseWriter, r *http.Request, session loginSession) bool {
	switch r.URL.Path {
	case "/api/maintenance":
		if r.Method != "GET" {
			w.WriteHeader(405)
			return true
		}
		v := map[string]any{"version": Version, "available": h.ops != nil, "restore_limit": MaxBackupBytes}
		if h.ops != nil {
			job, e := h.ops.Status(r.Context())
			if e != nil {
				v["available"] = false
				v["error"] = e.Error()
			} else {
				v["job"] = job
			}
		}
		jsonReply(w, 200, v)
		return true
	case "/api/updates/check":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return true
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		release, e := LatestPanelRelease(ctx)
		if e != nil {
			errorReply(w, 502, e)
			return true
		}
		v := strings.TrimPrefix(release.Tag, "panel-v")
		jsonReply(w, 200, map[string]any{"current": Version, "latest": v, "available": NewerVersion(v, Version), "release_url": release.URL, "notes": release.Notes})
		return true
	case "/api/updates/install":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return true
		}
		var in struct {
			Version  string `json:"version"`
			Password string `json:"password"`
		}
		if e := decodeJSON(w, r, &in); e != nil {
			errorReply(w, 400, e)
			return true
		}
		if !h.maintenancePassword(w, r, in.Password) {
			return true
		}
		if !NewerVersion(in.Version, Version) {
			errorReply(w, 400, errors.New("choose a newer official panel version"))
			return true
		}
		if h.ops == nil {
			errorReply(w, 503, errors.New("maintenance service unavailable"))
			return true
		}
		job, e := h.ops.Start(r.Context(), MaintenanceRequest{Action: "update", Version: in.Version})
		if e != nil {
			errorReply(w, 409, e)
		} else {
			_ = h.store.Audit("maintenance.update_requested", "")
			jsonReply(w, 202, job)
		}
		return true
	case "/api/restore/preview":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return true
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxBackupBytes)
		raw, e := io.ReadAll(r.Body)
		if e != nil {
			errorReply(w, 413, errors.New("backup must be at most 32 MiB"))
			return true
		}
		f, e := os.CreateTemp(filepath.Dir(h.store.db.Path()), "restore-preview-*.db")
		if e != nil {
			errorReply(w, 500, errors.New("cannot stage backup"))
			return true
		}
		path := f.Name()
		defer os.Remove(path)
		_ = f.Chmod(0600)
		_, e = f.Write(raw)
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			errorReply(w, 500, errors.New("cannot stage backup"))
			return true
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		var summary BackupSummary
		if h.inspectBackup != nil {
			summary, e = h.inspectBackup(ctx, path)
		} else {
			summary, e = InspectBackupFile(path)
		}
		if e != nil {
			errorReply(w, 400, errors.New("invalid or unsupported panel backup"))
			return true
		}
		cfg, e := h.store.Settings()
		if e != nil {
			errorReply(w, 500, e)
			return true
		}
		if summary.BaseZone != cfg.BaseZone {
			errorReply(w, 400, BackupMismatch(summary.BaseZone, cfg.BaseZone))
			return true
		}
		key := token()
		h.mu.Lock()
		h.preview = &restorePreview{key, session.csrf, raw, summary, time.Now().Add(10 * time.Minute)}
		h.mu.Unlock()
		jsonReply(w, 200, map[string]any{"token": key, "summary": summary, "preserve_admin": true, "preserve_access": true})
		return true
	case "/api/restore/apply":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return true
		}
		var in struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if e := decodeJSON(w, r, &in); e != nil {
			errorReply(w, 400, e)
			return true
		}
		if !h.maintenancePassword(w, r, in.Password) {
			return true
		}
		h.mu.Lock()
		p := h.preview
		if p != nil && p.Token == in.Token && p.CSRF == session.csrf && time.Now().Before(p.Expires) {
			h.preview = nil
		} else {
			p = nil
		}
		h.mu.Unlock()
		if p == nil {
			errorReply(w, 400, errors.New("backup preview expired; upload the file again"))
			return true
		}
		if h.ops == nil {
			errorReply(w, 503, errors.New("maintenance service unavailable"))
			return true
		}
		job, e := h.ops.Start(r.Context(), MaintenanceRequest{Action: "restore", Backup: p.Data, SHA256: p.Summary.SHA256, ExpectedZone: p.Summary.BaseZone})
		if e != nil {
			errorReply(w, 409, e)
		} else {
			_ = h.store.Audit("maintenance.restore_requested", "")
			jsonReply(w, 202, job)
		}
		return true
	}
	return false
}
