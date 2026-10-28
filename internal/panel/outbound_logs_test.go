package panel

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOutboundJournalRetentionStorageAndReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outbound")
	j := newOutboundJournal(dir)
	cfg := OutboundLogConfig{Enabled: true, RetentionHours: 1, MaxMiB: 4}
	j.configure(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	now := time.Now().UnixMilli()
	for i := 0; i < 7000; i++ {
		j.enqueue(OutboundRecord{ID: fmt.Sprint(i), Run: j.run, StartedAt: now, Engine: "soar", AccountID: "account", Host: strings.Repeat("a", 230) + ".example", Port: 443, State: "completed", Route: "direct", Attempts: []OutboundAttempt{{IP: "8.8.8.8", Result: "connected"}}})
		if i%100 == 0 {
			if err := j.flush(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	j.enqueue(OutboundRecord{ID: "active-before-restart", Run: j.run, StartedAt: now, Engine: "masterdns", Host: "example.org", Port: 443, State: "active"})
	j.close()
	entries, _ := j.segments()
	var size int64
	for _, entry := range entries {
		info, _ := entry.Info()
		size += info.Size()
	}
	if size > 4<<20 {
		t.Fatal("journal exceeded hard cap", size)
	}
	if j.dropped.Load() != 0 || j.storageErrors.Load() != 0 {
		t.Fatal("unexpected journal loss", j.dropped.Load(), j.storageErrors.Load())
	}
	reopened := newOutboundJournal(dir)
	reopened.configure(cfg)
	defer reopened.close()
	rows, err := reopened.query(ctx, outboundQuery{From: now - 1, Until: now + 1, Engine: "masterdns"})
	if err != nil || len(rows) != 1 || rows[0].State != "interrupted" {
		t.Fatal(rows, err)
	}
	reopened.close()
	for _, entry := range entries {
		past := time.Now().Add(-2 * time.Hour)
		if err := os.Chtimes(filepath.Join(dir, entry.Name()), past, past); err != nil {
			t.Fatal(err)
		}
	}
	if err := reopened.prune(""); err != nil {
		t.Fatal(err)
	}
	remaining, _ := reopened.segments()
	if len(remaining) != 0 {
		t.Fatal("expired segments retained", len(remaining))
	}
}

func TestOutboundJournalBackpressureNeverBlocksTraffic(t *testing.T) {
	j := newOutboundJournal(filepath.Join(t.TempDir(), "outbound"))
	j.cfg.Enabled = true
	for i := 0; i < cap(j.queue)+50; i++ {
		j.enqueue(OutboundRecord{ID: fmt.Sprint(i)})
	}
	if j.dropped.Load() != 50 {
		t.Fatal("missing visible queue loss count", j.dropped.Load())
	}
	j.close()
}

func TestOutboundExactTimeFiltersPaginationAndWholeExport(t *testing.T) {
	b, store, h := testBrowser(t)
	b.login()
	j := h.manager.outbound
	cfg, err := store.Egress()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Logs = OutboundLogConfig{Enabled: true, RetentionHours: 24, MaxMiB: 4}
	if err := store.SaveEgress(cfg); err != nil {
		t.Fatal(err)
	}
	if err := h.manager.RestartEngines(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	start := now - 600000
	for i := 0; i < 301; i++ {
		j.enqueue(OutboundRecord{ID: fmt.Sprint(i), Run: j.run, Engine: "cotten", AccountID: "one", Host: "=formula.example", DestinationIP: "8.8.8.8", Port: 8443, Network: "tcp", Route: "socks5", StartedAt: start + int64(i), EndedAt: start + int64(i) + 100, State: "completed", UploadBytes: 1, DownloadBytes: 2})
	}
	j.enqueue(OutboundRecord{ID: "different-engine", Run: j.run, Engine: "masterdns", StartedAt: start + 10, State: "failed"})
	url := "/api/outbound?from=" + strconv.FormatInt(start, 10) + "&until=" + strconv.FormatInt(start+300, 10) + "&engine=cotten"
	var report struct {
		Entries []OutboundRecord `json:"entries"`
		Total   int              `json:"total"`
		Next    int              `json:"next_offset"`
	}
	if err := json.Unmarshal(b.request(url, "GET", nil, 200), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 200 || report.Total != 301 || report.Next != 200 {
		t.Fatalf("page=%d total=%d next=%d", len(report.Entries), report.Total, report.Next)
	}
	if err := json.Unmarshal(b.request(url+"&offset=200", "GET", nil, 200), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 101 || report.Next != -1 {
		t.Fatal("pagination incomplete", len(report.Entries), report.Next)
	}
	raw := b.request(url+"&format=csv", "GET", nil, 200)
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(raw), "\ufeff"))).ReadAll()
	if err != nil || len(rows) != 302 {
		t.Fatal("export silently paginated", len(rows), err)
	}
	if rows[1][5] != "'=formula.example" {
		t.Fatal("CSV formula injection not escaped", rows[1][5])
	}
	var full struct {
		Entries []OutboundRecord `json:"entries"`
	}
	json.Unmarshal(b.request(url+"&format=json&download=1", "GET", nil, 200), &full)
	if len(full.Entries) != 301 {
		t.Fatal("JSON export silently paginated", len(full.Entries))
	}
	boundary := url + "&state=failed"
	json.Unmarshal(b.request(boundary, "GET", nil, 200), &report)
	if report.Total != 0 {
		t.Fatal("state filter ignored")
	}
	for _, path := range []string{"/api/outbound?from=bad", "/api/outbound?from=200&until=100", "/api/outbound?limit=999999", "/api/outbound?offset=-1"} {
		b.request(path, "GET", nil, 400)
	}
}

func TestOutboundObservedConnCloseUnblocksReadAndPreservesCounts(t *testing.T) {
	j := newOutboundJournal(filepath.Join(t.TempDir(), "outbound"))
	j.configure(OutboundLogConfig{Enabled: true, RetentionHours: 1, MaxMiB: 4})
	defer j.close()
	f := j.begin("account", upstreamSoar, "tcp", "8.8.8.8:443", EngineEgress{Mode: "direct"}, false)
	f.opened()
	a, b := net.Pipe()
	defer b.Close()
	conn := &outboundObservedConn{Conn: a, flow: f}
	done := make(chan struct{})
	go func() { defer close(done); buf := make([]byte, 10); conn.Read(buf) }()
	b.Write([]byte("four"))
	<-done
	go conn.Read(make([]byte, 10))
	closed := make(chan struct{})
	go func() { conn.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close deadlocked with outstanding read")
	}
	r := f.snapshot()
	if r.DownloadBytes != 4 || r.State != "completed" {
		t.Fatal(r)
	}
}

func TestOutboundCSVFormulaPrefixes(t *testing.T) {
	for _, raw := range []string{"=1+1", " \t=1+1", "\n@cmd", "\r-1", "\x00=1", "+1"} {
		if !strings.HasPrefix(csvSafe(raw), "'") {
			t.Fatal("unsafe spreadsheet cell", strconv.Quote(raw))
		}
	}
	if csvSafe("example.org") != "example.org" {
		t.Fatal("ordinary destination modified")
	}
}

func TestOutboundCheckpointSurvivesInterruptedConnection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outbound")
	cfg := OutboundLogConfig{Enabled: true, RetentionHours: 1, MaxMiB: 4}
	j := newOutboundJournal(dir)
	j.configure(cfg)
	flow := j.begin("account", upstreamMasterDNS, "tcp", "8.8.8.8:443", EngineEgress{Mode: "direct"}, false)
	flow.opened()
	flow.up.Add(17)
	flow.down.Add(25)
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	checkpointed := false
	for ctx.Err() == nil {
		if err := j.flush(ctx); err != nil {
			break
		}
		entries, _ := j.segments()
		for _, entry := range entries {
			raw, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
			if strings.Contains(string(raw), `"upload_bytes":17`) {
				checkpointed = true
				break
			}
		}
		if checkpointed {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	j.close()
	if !checkpointed {
		t.Fatal("active connection was never checkpointed")
	}
	reopened := newOutboundJournal(dir)
	reopened.configure(cfg)
	defer reopened.close()
	rows, err := reopened.query(context.Background(), outboundQuery{From: time.Now().Add(-time.Hour).UnixMilli(), Until: time.Now().UnixMilli()})
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	r := rows[0]
	if r.State != "interrupted" || r.BytesKnown || r.EndedAt != 0 || r.UploadBytes != 17 || r.DownloadBytes != 25 || r.UpdatedAt <= r.StartedAt {
		t.Fatal("interrupted counters presented as complete", r)
	}
}

func TestOutboundCorruptTailPreservesValidHistoryWithWarning(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outbound")
	cfg := OutboundLogConfig{Enabled: true, RetentionHours: 1, MaxMiB: 4}
	j := newOutboundJournal(dir)
	j.configure(cfg)
	now := time.Now().UnixMilli()
	j.enqueue(OutboundRecord{ID: "retained", Run: j.run, StartedAt: now, Engine: "soar", Port: 443, State: "completed", BytesKnown: true})
	j.close()
	entries, _ := j.segments()
	file, err := os.OpenFile(filepath.Join(dir, entries[0].Name()), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString(`{"id":"incomplete`)
	file.Close()
	reopened := newOutboundJournal(dir)
	reopened.configure(cfg)
	defer reopened.close()
	rows, err := reopened.query(context.Background(), outboundQuery{From: now - 1, Until: now + 1})
	if err != nil || len(rows) != 1 || rows[0].ID != "retained" || reopened.storageErrors.Load() == 0 {
		t.Fatal("corrupt tail hid valid evidence or was silently ignored", rows, err, reopened.storageErrors.Load())
	}
}
