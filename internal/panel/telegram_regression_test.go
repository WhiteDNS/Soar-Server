package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type telegramGatedReader struct {
	r               io.Reader
	started, resume chan struct{}
	once            sync.Once
}

func (r *telegramGatedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started); <-r.resume })
	return r.r.Read(p)
}

func TestTelegramDelayedSettingsPreserveCredentialChanges(t *testing.T) {
	for _, change := range []string{"disconnect", "replacement", "replacement_blank_token"} {
		t.Run(change, func(t *testing.T) {
			bot, _, _ := newTelegramTest(t)
			body := `{"alerts":false}`
			if change == "replacement_blank_token" {
				body = `{"alerts":false,"token":""}`
			}
			gate := &telegramGatedReader{r: strings.NewReader(body), started: make(chan struct{}), resume: make(chan struct{})}
			req := httptest.NewRequest("PUT", "http://panel.example/api/telegram", gate)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); bot.h.telegramAPI(response, req) }()
			var release sync.Once
			defer func() { release.Do(func() { close(gate.resume) }); <-done }()
			<-gate.started
			wantToken, wantStatus := "", http.StatusBadRequest
			if change == "disconnect" {
				if e := bot.disconnect(); e != nil {
					t.Fatal(e)
				}
			} else {
				wantToken, wantStatus = "987654321:abcdefghijklmnopqrstuvwxyz_123456789", http.StatusOK
				cfg := bot.config()
				cfg.Token = wantToken
				if e := bot.configure(context.Background(), cfg); e != nil {
					t.Fatal(e)
				}
			}
			release.Do(func() { close(gate.resume) })
			<-done
			if response.Code != wantStatus {
				t.Fatalf("settings status: want %d got %d", wantStatus, response.Code)
			}
			cfg := bot.config()
			if cfg.Token != wantToken || cfg.ChatID != 0 || (change == "disconnect" && cfg.Enabled) {
				t.Fatal("delayed settings restored revoked credentials or administrator")
			}
			reloaded, e := NewTelegram(bot.h, bot.file)
			if e != nil || reloaded.config() != cfg {
				t.Fatal("credential change not preserved on disk", e)
			}
		})
	}
}

func TestTelegramFailedUpdatePersistenceRetriesExactlyOnce(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	original := bot.file
	block := filepath.Join(t.TempDir(), "not-a-directory")
	if e := os.WriteFile(block, []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	bot.file = filepath.Join(block, "telegram.json")
	u := tgCallback(1, "new")
	if e := bot.handleUpdate(context.Background(), u); !errors.Is(e, errTelegramUpdatePersistence) {
		t.Fatal("missing persistence error", e)
	}
	if bot.state.Offset != 0 || bot.conversation != nil || len(f.texts) != 0 {
		t.Fatal("failed persistence consumed or executed the update")
	}
	bot.file = original
	mustHandle(t, bot, u)
	if bot.conversation == nil || len(f.texts) != 1 {
		t.Fatal("update not dispatched after storage repair")
	}
	reloaded, e := NewTelegram(bot.h, bot.file)
	if e != nil {
		t.Fatal(e)
	}
	reloaded.baseURL, reloaded.client, reloaded.sendInterval = bot.baseURL, bot.client, 0
	mustHandle(t, reloaded, u)
	if len(f.texts) != 1 || reloaded.state.Offset != 2 {
		t.Fatal("durably dispatched update replayed after restart")
	}
}

func TestTelegramWorkerRetainsBatchAfterUpdatePersistenceFailure(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	bot.cfg.Language, bot.cfg.Alerts, bot.cfg.DailyReport = "en", false, false
	bot.state.Booted = true
	if e := bot.save(); e != nil {
		t.Fatal(e)
	}
	f.updates = []telegramUpdate{tgMessage(1, "/menu"), tgMessage(2, "/stats")}
	offsets := make(chan int64, 8)
	var pollMu sync.Mutex
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			pollMu.Lock()
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var request struct{ Offset int64 }
			_ = json.Unmarshal(raw, &request)
			polls++
			if polls == 1 {
				if e := os.Rename(bot.file, bot.file+".saved"); e != nil {
					t.Error(e)
				}
				if e := os.Mkdir(bot.file, 0700); e != nil {
					t.Error(e)
				}
				select {
				case bot.wake <- struct{}{}:
				default:
				}
			} else if polls == 2 {
				if e := os.Remove(bot.file); e != nil {
					t.Error(e)
				}
				if e := os.Rename(bot.file+".saved", bot.file); e != nil {
					t.Error(e)
				}
			}
			select {
			case offsets <- request.Offset:
			default:
			}
			pollMu.Unlock()
		}
		f.serve(w, r)
	}))
	t.Cleanup(server.Close)
	bot.baseURL, bot.client = server.URL, server.Client()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); bot.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	for range 2 {
		select {
		case offset := <-offsets:
			if offset != 0 {
				t.Fatalf("unexecuted batch acknowledged with offset %d", offset)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("worker did not retry failed batch")
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		count := len(f.texts)
		f.mu.Unlock()
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want both commands once, got %d replies", count)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTelegramFirstMissedReportSurvivesRestartWithoutBacklog(t *testing.T) {
	for _, outage := range []string{"failed_send", "offline_before_first_report"} {
		t.Run(outage, func(t *testing.T) {
			bot, f, _ := newTelegramTest(t)
			bot.cfg.Language, bot.cfg.Alerts = "en", false
			if e := bot.save(); e != nil {
				t.Fatal(e)
			}
			started := bot.state.ReportStarted
			loc, _ := time.LoadLocation("Asia/Tehran")
			now := time.Date(2026, 10, 24, 8, 59, 0, 0, loc)
			bot.now = func() time.Time { return now }
			bot.tick(context.Background())
			if len(f.texts) != 0 {
				t.Fatal("first report sent before configured time")
			}
			if outage == "failed_send" {
				now = now.Add(time.Minute)
				f.fail = "sendMessage"
				bot.tick(context.Background())
				if bot.state.LastReport != "" {
					t.Fatal("failed report marked delivered")
				}
				f.fail = ""
			}
			reloaded, e := NewTelegram(bot.h, bot.file)
			if e != nil {
				t.Fatal(e)
			}
			if started == 0 || reloaded.state.ReportStarted != started {
				t.Fatal("first report baseline not durable")
			}
			reloaded.client, reloaded.baseURL, reloaded.sendInterval = bot.client, bot.baseURL, 0
			reloaded.latest, reloaded.now = bot.latest, bot.now
			now = time.Date(2026, 10, 28, 8, 0, 0, 0, loc)
			reloaded.tick(context.Background())
			reloaded.tick(context.Background())
			if len(f.texts) != 1 || reloaded.state.LastReport != "2026-10-27" {
				t.Fatal("missed first report not recovered once without backlog", f.texts)
			}
			now = now.Add(time.Hour)
			reloaded.tick(context.Background())
			if len(f.texts) != 2 || reloaded.state.LastReport != "2026-10-28" {
				t.Fatal("regular 09:00 Tehran report missed after catch-up")
			}
		})
	}
}

func TestTelegramFirstReportBaselineFromPreferencesAndOlderState(t *testing.T) {
	bot, _, _ := newTelegramTest(t)
	bot.cfg.DailyReport = false
	bot.state.ReportStarted = 0
	if e := bot.preference(context.Background(), "report"); e != nil {
		t.Fatal(e)
	}
	if bot.state.ReportStarted != bot.now().UnixMilli() {
		t.Fatal("enabling reports did not establish a baseline")
	}
	bot.state.ReportStarted, bot.state.Booted = 0, true
	if e := bot.save(); e != nil {
		t.Fatal(e)
	}
	before := time.Now().UnixMilli()
	reloaded, e := NewTelegram(bot.h, bot.file)
	if e != nil {
		t.Fatal(e)
	}
	if reloaded.state.ReportStarted < before || reloaded.state.Booted || !reloaded.recovered {
		t.Fatal("older state did not schedule durable initialization before polling")
	}
	if e := reloaded.save(); e != nil {
		t.Fatal(e)
	}
	again, e := NewTelegram(bot.h, bot.file)
	if e != nil || again.state.ReportStarted != reloaded.state.ReportStarted {
		t.Fatal("migrated baseline not durable", e)
	}
}
