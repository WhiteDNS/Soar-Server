package panel

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (h *HTTP) egressAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		cfg, err := h.store.Egress()
		if err != nil {
			errorReply(w, 500, errors.New("cannot read outbound configuration"))
			return
		}
		jsonReply(w, 200, cfg.publicView())
	case http.MethodPut:
		var cfg EgressConfig
		if err := decodeJSON(w, r, &cfg); err != nil {
			errorReply(w, 400, err)
			return
		}
		if err := h.store.SaveEgress(cfg); err != nil {
			status := 400
			if errors.Is(err, ErrConflict) {
				status = 409
			}
			errorReply(w, status, err)
			return
		}
		if err := h.manager.RestartEngines(); err != nil {
			errorReply(w, 500, errors.New("saved outbound settings; runtime synchronization failed"))
			return
		}
		saved, err := h.store.Egress()
		if err != nil {
			errorReply(w, 500, errors.New("cannot read saved outbound configuration"))
			return
		}
		jsonReply(w, 200, saved.publicView())
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func outboundTime(raw string, fallback int64) (int64, error) {
	if raw == "" {
		return fallback, nil
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 {
		return n, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil && t.UnixMilli() >= 0 {
		return t.UnixMilli(), nil
	}
	return 0, errors.New("use a nonnegative millisecond timestamp or RFC3339 date with timezone")
}
func csvSafe(s string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(s, "\x00", ""))
	if strings.HasPrefix(trimmed, "=") || strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "@") || strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "\r") || strings.HasPrefix(s, "\n") || strings.Contains(s, "\x00") {
		return "'" + s
	}
	return s
}
func timestampUTC(value int64) string {
	if value == 0 {
		return ""
	}
	return time.UnixMilli(value).UTC().Format(time.RFC3339Nano)
}

func (h *HTTP) outboundAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	if h.manager.outbound == nil {
		errorReply(w, 503, errors.New("outbound reporting unavailable"))
		return
	}
	v := r.URL.Query()
	now := time.Now().UnixMilli()
	from, err := outboundTime(v.Get("from"), now-int64(time.Hour/time.Millisecond))
	if err != nil {
		errorReply(w, 400, err)
		return
	}
	until, err := outboundTime(v.Get("until"), now)
	if err != nil || until < from {
		errorReply(w, 400, errors.New("invalid outbound time range"))
		return
	}
	q := outboundQuery{From: from, Until: until, Engine: v.Get("engine"), Account: v.Get("account"), State: v.Get("state"), Route: v.Get("route")}
	if len(q.Account) > 64 || !oneOf(q.Engine, "", "soar", "cotten", "masterdns") || !oneOf(q.Route, "", "direct", "socks5", "http", "https") || !oneOf(q.State, "", "active", "completed", "failed", "denied", "interrupted") {
		errorReply(w, 400, errors.New("invalid outbound filter"))
		return
	}
	format := v.Get("format")
	if !oneOf(format, "", "json", "csv") {
		errorReply(w, 400, errors.New("choose JSON or CSV"))
		return
	}
	limit, offset := 200, 0
	if v.Has("limit") {
		limit, err = strconv.Atoi(v.Get("limit"))
		if err != nil || limit < 1 || limit > 2000 {
			errorReply(w, 400, errors.New("outbound page limit must be 1–2000"))
			return
		}
	}
	if v.Has("offset") {
		offset, err = strconv.Atoi(v.Get("offset"))
		if err != nil || offset < 0 || offset > outboundQueryMax {
			errorReply(w, 400, errors.New("invalid outbound offset"))
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rows, err := h.manager.outbound.query(ctx, q)
	if err != nil {
		errorReply(w, 503, err)
		return
	}
	j := h.manager.outbound
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Outbound-Dropped", strconv.FormatUint(j.dropped.Load(), 10))
	w.Header().Set("X-Outbound-Storage-Errors", strconv.FormatUint(j.storageErrors.Load(), 10))
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="whitedns-outbound.csv"`)
		fmt.Fprint(w, "\ufeff")
		writer := csv.NewWriter(w)
		writer.Write([]string{"connection_id", "engine", "account_id", "started_utc", "ended_utc", "host", "destination_ip", "port", "protocol", "purpose", "route", "proxy_endpoint", "upload_bytes", "download_bytes", "state", "reason", "attempts", "journal_dropped", "journal_storage_errors", "last_observed_utc", "bytes_known"})
		for _, row := range rows {
			if ctx.Err() != nil {
				return
			}
			attempts := make([]string, 0, len(row.Attempts))
			for _, a := range row.Attempts {
				attempts = append(attempts, a.IP+":"+a.Result)
			}
			fields := []string{row.ID, row.Engine, row.AccountID, timestampUTC(row.StartedAt), timestampUTC(row.EndedAt), row.Host, row.DestinationIP, strconv.Itoa(int(row.Port)), row.Network, row.Purpose, row.Route, row.Proxy, strconv.FormatUint(row.UploadBytes, 10), strconv.FormatUint(row.DownloadBytes, 10), row.State, row.Reason, strings.Join(attempts, ";"), strconv.FormatUint(j.dropped.Load(), 10), strconv.FormatUint(j.storageErrors.Load(), 10), timestampUTC(row.UpdatedAt), strconv.FormatBool(row.BytesKnown)}
			for i := range fields {
				fields[i] = csvSafe(fields[i])
			}
			if err := writer.Write(fields); err != nil {
				return
			}
		}
		writer.Flush()
		return
	}
	if format == "json" && v.Get("download") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="whitedns-outbound.json"`)
		jsonReply(w, 200, map[string]any{"entries": rows, "total": len(rows), "from": from, "until": until, "dropped": j.dropped.Load(), "storage_errors": j.storageErrors.Load(), "logging": j.config(), "time_filter": "connection_started_at", "byte_scope": "whole_connection_payload"})
		return
	}
	var up, down uint64
	for _, row := range rows {
		up += row.UploadBytes
		down += row.DownloadBytes
	}
	end := min(len(rows), offset+limit)
	offset = min(offset, len(rows))
	next := -1
	if end < len(rows) {
		next = end
	}
	jsonReply(w, 200, map[string]any{"entries": rows[offset:end], "total": len(rows), "next_offset": next, "from": from, "until": until, "upload_bytes": up, "download_bytes": down, "dropped": j.dropped.Load(), "storage_errors": j.storageErrors.Load(), "pending": len(j.queue), "logging": j.config(), "time_filter": "connection_started_at", "byte_scope": "whole_connection_payload"})
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
