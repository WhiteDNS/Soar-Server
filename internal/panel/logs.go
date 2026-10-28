package panel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const diagnosticLogCapacity = 2000

// DiagnosticLog deliberately contains no free-form engine message, destination,
// DNS question, credential or payload. Codes are authored at the owning callsite.
type DiagnosticLog struct {
	ID         uint64 `json:"id"`
	At         int64  `json:"at"`
	LastAt     int64  `json:"last_at"`
	Level      string `json:"level"`
	Source     string `json:"source"`
	AccountID  string `json:"account_id,omitempty"`
	Code       string `json:"code"`
	Reason     string `json:"reason,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Count      uint64 `json:"count"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Operation  string `json:"operation,omitempty"`
}

type diagnosticLogs struct {
	mu              sync.Mutex
	entries         []DiagnosticLog
	head            int
	next            uint64
	coalesced       uint64
	revision, saved uint64
	file            string
	storageError    bool
	flushMu         sync.Mutex
}

func (m *Manager) logEvent(level, source, account, code string, err error, elapsed time.Duration) {
	m.logs.add(DiagnosticLog{Level: level, Source: source, AccountID: account, Code: code, Reason: diagnosticReason(err), DurationMS: elapsed.Milliseconds()})
}

func (l *diagnosticLogs) add(entry DiagnosticLog) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now().UnixMilli()
	l.revision++
	// Aggregate bursts in a bounded window without making the traffic path wait
	// for disk or network I/O. Keep the latest latency and the full occurrence count.
	for step := 0; step < min(len(l.entries), 64); step++ {
		i := (l.head + len(l.entries) - 1 - step) % len(l.entries)
		old := &l.entries[i]
		if now-old.At > 3000 {
			break
		}
		if old.Level == entry.Level && old.Source == entry.Source && old.AccountID == entry.AccountID && old.Code == entry.Code && old.Reason == entry.Reason && old.HTTPStatus == entry.HTTPStatus && old.Operation == entry.Operation {
			old.Count++
			old.LastAt = now
			old.DurationMS = entry.DurationMS
			l.coalesced++
			return
		}
	}
	l.next++
	entry.ID, entry.At, entry.LastAt, entry.Count = l.next, now, now, 1
	if len(l.entries) == diagnosticLogCapacity {
		l.entries[l.head] = entry
		l.head = (l.head + 1) % diagnosticLogCapacity
	} else {
		l.entries = append(l.entries, entry)
	}
}

func (l *diagnosticLogs) open(path string) {
	l.file = path
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		l.storageError = true
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 4*1024*1024+1))
	var entries []DiagnosticLog
	if err != nil || len(raw) > 4*1024*1024 || json.Unmarshal(raw, &entries) != nil || len(entries) > diagnosticLogCapacity {
		l.storageError = true
		return
	}
	for _, e := range entries {
		if e.ID <= l.next || e.Count == 0 || len(e.Code) > 80 || len(e.Reason) > 80 || len(e.AccountID) > 64 || len(e.Source) > 16 || len(e.Level) > 8 {
			l.storageError = true
			return
		}
		l.next = e.ID
	}
	l.entries = entries
}

func (l *diagnosticLogs) flush() {
	l.flushMu.Lock()
	defer l.flushMu.Unlock()
	l.mu.Lock()
	if l.file == "" || l.revision == l.saved {
		l.mu.Unlock()
		return
	}
	entries := append([]DiagnosticLog{}, l.entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	revision := l.revision
	file := l.file
	l.mu.Unlock()
	err := WritePrivateJSON(file, entries)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.storageError = err != nil
	if err == nil {
		l.saved = revision
	}
}

func diagnosticReason(err error) string {
	if err == nil {
		return ""
	}
	var dns *net.DNSError
	var op *net.OpError
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrDenied):
		return "account_inactive"
	case errors.Is(err, errAccountStreamLimit):
		return "account_stream_limit"
	case errors.Is(err, errServerStreamCapacity):
		return "server_stream_limit"
	case errors.Is(err, errDestinationPort):
		return "destination_policy"
	case errors.Is(err, errUpstreamProxy):
		return "proxy_unavailable"
	case errors.Is(err, errProxyUDP):
		return "proxy_udp_unsupported"
	case errors.Is(err, errAntiFlood):
		return "antiflood"
	case errors.As(err, &dns):
		if dns.Timeout() {
			return "dns_timeout"
		}
		if dns.IsNotFound {
			return "dns_not_found"
		}
		return "dns_failure"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection_refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return "network_unreachable"
	case errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE):
		return "file_descriptor_limit"
	case errors.As(err, &op) && op.Timeout():
		return "timeout"
	case errors.Is(err, net.ErrClosed):
		return "closed"
	default:
		return "internal_error"
	}
}

func (c *meterConn) logStreamError(code string, err error) {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) || c.rt.ctx.Err() != nil {
		return
	}
	c.manager.logEvent("warn", engineLogSource(c.engine), c.rt.client.ID, code, err, 0)
}

func engineLogSource(engine upstreamEngine) string {
	switch engine {
	case upstreamSoar:
		return "soar"
	case upstreamCotten:
		return "cotten"
	case upstreamMasterDNS:
		return "masterdns"
	default:
		return "connection"
	}
}

type logQuery struct {
	Source, Level, Account, Search string
	Since                          int64
	Limit                          int
}

func (l *diagnosticLogs) snapshot(q logQuery) (out []DiagnosticLog, retained int, coalesced uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out = []DiagnosticLog{}
	if q.Limit <= 0 {
		q.Limit = 200
	}
	for i := len(l.entries) - 1; i >= 0; i-- {
		e := l.entries[i]
		if q.Source != "" && e.Source != q.Source || q.Level != "" && e.Level != q.Level || q.Account != "" && e.AccountID != q.Account || e.LastAt < q.Since {
			continue
		}
		if q.Search != "" && !strings.Contains(strings.ToLower(e.Code+" "+e.Reason+" "+e.Source+" "+e.AccountID), strings.ToLower(q.Search)) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastAt == out[j].LastAt {
			return out[i].ID > out[j].ID
		}
		return out[i].LastAt > out[j].LastAt
	})
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, len(l.entries), l.coalesced
}

func (h *HTTP) logsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	v := r.URL.Query()
	q := logQuery{Source: v.Get("source"), Level: v.Get("level"), Account: v.Get("account"), Search: v.Get("q"), Limit: 200}
	var err error
	if v.Has("limit") {
		q.Limit, err = strconv.Atoi(v.Get("limit"))
		if err != nil || q.Limit < 1 || q.Limit > diagnosticLogCapacity {
			errorReply(w, 400, errors.New("invalid log limit"))
			return
		}
	}
	if v.Has("since") {
		q.Since, err = strconv.ParseInt(v.Get("since"), 10, 64)
		if err != nil || q.Since < 0 {
			errorReply(w, 400, errors.New("invalid log time"))
			return
		}
	}
	if len(q.Search) > 160 || len(q.Account) > 64 {
		errorReply(w, 400, errors.New("invalid log filter"))
		return
	}
	logs, retained, coalesced := h.manager.logs.snapshot(q)
	h.manager.logs.mu.Lock()
	storageError := h.manager.logs.storageError
	h.manager.logs.mu.Unlock()
	jsonReply(w, 200, map[string]any{"entries": logs, "retained": retained, "capacity": diagnosticLogCapacity, "coalesced": coalesced, "started_at": h.manager.started.UnixMilli(), "storage_error": storageError, "runtime": h.manager.Diagnostics()})
}

func diagnosticLogPath(s *Store) string {
	return filepath.Join(filepath.Dir(s.db.Path()), "diagnostics.json")
}

type diagnosticResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *diagnosticResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *diagnosticResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *diagnosticResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (h *HTTP) logRequest(w *diagnosticResponseWriter, r *http.Request) {
	if w.status < 400 && (r.Method == "GET" || r.Method == "HEAD") {
		return
	}
	code := "request_completed"
	level := "info"
	if w.status >= 400 {
		code = "request_rejected"
		level = "warn"
	}
	if w.status >= 500 {
		code = "request_failed"
		level = "error"
	}
	// Keep only a fixed route category: private paths, tokens, queries and
	// request/response bodies must never become diagnostic messages.
	category := "panel"
	for _, p := range []string{"settings", "clients", "assistant", "antiflood", "backup", "maintenance", "telegram", "access", "admin"} {
		if strings.HasPrefix(r.URL.Path, "/api/"+p) {
			category = p
			break
		}
	}
	method := r.Method
	switch method {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
	default:
		method = "OTHER"
	}
	h.manager.logs.add(DiagnosticLog{Level: level, Source: "panel", Code: category + "_" + code, HTTPStatus: w.status, Operation: method})
}

func (m *Manager) logSummary() map[string]any {
	entries, _, _ := m.logs.snapshot(logQuery{Since: time.Now().Add(-5 * time.Minute).UnixMilli(), Limit: diagnosticLogCapacity})
	var errorsCount, warnings uint64
	latest := []DiagnosticLog{}
	for _, e := range entries {
		if e.Level == "error" {
			errorsCount += e.Count
		}
		if e.Level == "warn" {
			warnings += e.Count
		}
		if e.Level != "info" && len(latest) < 3 {
			latest = append(latest, e)
		}
	}
	return map[string]any{"errors": errorsCount, "warnings": warnings, "latest": latest}
}
