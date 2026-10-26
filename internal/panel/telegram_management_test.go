package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func replyToBot(bot *Telegram, text string) telegramMessage {
	m := *tgMessage(1, text).Message
	m.Reply = &struct {
		ID int64 `json:"message_id"`
	}{bot.conversation.Prompt}
	return m
}

func TestTelegramEncryptedBackupRoundTripAndTampering(t *testing.T) {
	raw := bytes.Repeat([]byte("snapshot with secret account keys\n"), 1000)
	key := token()
	first, e := sealTelegramBackup(raw, key)
	if e != nil {
		t.Fatal(e)
	}
	second, e := sealTelegramBackup(raw, key)
	if e != nil || bytes.Equal(first, second) || bytes.Contains(first, []byte("secret account keys")) {
		t.Fatal("backup not independently encrypted", e)
	}
	decoded, e := openTelegramBackup(first, key)
	if e != nil || !bytes.Equal(decoded, raw) {
		t.Fatal("encrypted backup did not round trip", e)
	}
	if _, e = openTelegramBackup(first, token()); e == nil {
		t.Fatal("wrong key accepted")
	}
	first[len(first)-1] ^= 1
	if _, e = openTelegramBackup(first, key); e == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if _, e = openTelegramBackup([]byte(telegramBackupMagic), key); e == nil {
		t.Fatal("truncated backup accepted")
	}
	if _, e = sealTelegramBackup(raw, "bad-key"); e == nil {
		t.Fatal("invalid key accepted")
	}
	if _, e = sealTelegramBackup(make([]byte, MaxBackupBytes+1), key); e == nil {
		t.Fatal("oversized snapshot accepted")
	}
}

func TestTelegramSchedulerExpiresRestorePreviews(t *testing.T) {
	now := time.Now()
	bot := &Telegram{now: func() time.Time { return now }, cfg: defaultTelegramConfig(), confirmation: &telegramConfirmation{Backup: make([]byte, 8192), Expires: now.Add(-time.Second)}, conversation: &telegramConversation{Expires: now.Add(-time.Second)}}
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	bot.runSchedule(ctx)
	if bot.confirmation != nil || bot.conversation != nil {
		t.Fatal("expired restore preview retained without further administrator input")
	}
}

func TestTelegramManualBackupConfirmationAndPrivateKeyPersistence(t *testing.T) {
	bot, f, b := newTelegramTest(t)
	ctx := context.Background()
	if e := bot.callback(ctx, "backup-now"); e != nil {
		t.Fatal(e)
	}
	if len(f.documents) != 0 {
		t.Fatal("backup sent without confirmation")
	}
	confirmation := bot.confirmation.Key
	if e := bot.execute(ctx, confirmation); e != nil {
		t.Fatal(e)
	}
	if len(f.documents) != 1 {
		t.Fatal("backup not uploaded")
	}
	if e := bot.execute(ctx, confirmation); e == nil || len(f.documents) != 1 {
		t.Fatal("backup confirmation replayed")
	}
	key := bot.config().BackupKey
	raw, e := openTelegramBackup(f.documents[0], key)
	if e != nil || bytes.Contains(raw, []byte(testTelegramToken)) || bytes.Contains(raw, []byte(key)) {
		t.Fatal("backup leaked machine-local Telegram credentials", e)
	}
	path := bot.file + ".snapshot.db"
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	defer os.Remove(path)
	if _, e = InspectBackupFile(path); e != nil {
		t.Fatal("exported snapshot is not restorable", e)
	}
	reloaded, e := NewTelegram(bot.h, bot.file)
	if e != nil || reloaded.config().BackupKey != key {
		t.Fatal("recovery key not durable", e)
	}
	b.login()
	view := b.request("/api/telegram", "GET", nil, 200)
	if bytes.Contains(view, []byte(key)) || bytes.Contains(view, []byte("backup_key")) {
		t.Fatal("recovery key exposed in public view")
	}
	if e := bot.disconnect(); e != nil {
		t.Fatal(e)
	}
	reloaded, e = NewTelegram(bot.h, bot.file)
	if e != nil || reloaded.config().Token != "" || reloaded.config().BackupKey != key {
		t.Fatal("disconnect destroyed backup recovery key", e)
	}
	if _, e = openTelegramBackup(f.documents[0], reloaded.config().BackupKey); e != nil {
		t.Fatal("backup no longer recoverable after disconnect", e)
	}
}

func TestTelegramPanelAccessPreparationAndActivation(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	a, e := NewAccessController(bot.file + ".access.json")
	if e != nil {
		t.Fatal(e)
	}
	a.lookup = func(context.Context, string) ([]string, error) { return []string{"192.0.2.10"}, nil }
	a.issue = func(string) error { return nil }
	bot.h.access = a
	ctx := context.Background()
	if e := bot.callback(ctx, "access-edit"); e != nil {
		t.Fatal(e)
	}
	if e := bot.message(ctx, replyToBot(bot, `{"domain":"panel.example.org","https":true,"aliases":[]}`)); e != nil {
		t.Fatal(e)
	}
	if a.Plan() != nil || a.Config().HTTPS {
		t.Fatal("access changed before confirmation")
	}
	if e := bot.execute(ctx, bot.confirmation.Key); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for a.Plan().State == "preparing" {
		if time.Now().After(deadline) {
			t.Fatal("preparation did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	if e := bot.accessMenu(ctx); e != nil {
		t.Fatal(e)
	}
	p := a.Plan()
	if e := bot.callback(ctx, "access-apply:"+p.ID); e != nil {
		t.Fatal(e)
	}
	if a.Config().HTTPS {
		t.Fatal("prepared address activated without second confirmation")
	}
	bot.h.sessions["old"] = loginSession{csrf: "old"}
	if e := bot.execute(ctx, bot.confirmation.Key); e != nil {
		t.Fatal(e)
	}
	if a.Config().Domain != "panel.example.org" || !a.Config().HTTPS || len(bot.h.sessions) != 0 {
		t.Fatal("access activation or session revocation failed")
	}
	for _, keyboard := range f.keyboards {
		for _, row := range keyboard.Rows {
			for _, button := range row {
				if len(button.Data) > 64 {
					t.Fatal("callback exceeds Telegram limit")
				}
			}
		}
	}
}

func TestTelegramServerSettingFields(t *testing.T) {
	for _, test := range []struct{ field, valid, invalid string }{
		{"resolvers", "192.0.2.53:53 198.51.100.53", "invalid"},
		{"allow_ports", "80 443 8443", "70000"},
		{"country", "GLOBAL", "unknown"},
		{"public_ip", "198.51.100.10", "invalid"},
		{"nameserver_host", "ns1.example.org", "invalid"},
		{"tunnel_zones", "soar.example.org t.example.net", "bad"},
		{"primary_zone", "soar.example.org", "unknown.example.net"},
		{"tunnel_nameservers", `{"soar.example.org":"ns1.example.org"}`, `{"bad":"invalid"}`},
		{"soarSettings", `{"transport":"off","maxInflight":64}`, `{"unknownField":123}`},
		{"antiflood", `{"mode":"monitor"}`, `{"mode":"invalid"}`},
	} {
		t.Run(test.field, func(t *testing.T) {
			bot, _, _ := newTelegramTest(t)
			ctx := context.Background()
			if e := bot.callback(ctx, "server:"+test.field); e != nil {
				t.Fatal(e)
			}
			if e := bot.message(ctx, replyToBot(bot, test.invalid)); e == nil {
				t.Fatal("invalid setting accepted")
			}
			if e := bot.message(ctx, replyToBot(bot, test.valid)); e != nil {
				t.Fatal(e)
			}
			if e := bot.execute(ctx, bot.confirmation.Key); e != nil {
				t.Fatal(e)
			}
			cfg, _ := bot.h.store.Settings()
			if e := cfg.Validate(); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestTelegramAutomaticBackupScheduleRetryCatchupAndRestart(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	loc, _ := time.LoadLocation("Asia/Tehran")
	now := time.Date(2026, 10, 24, 2, 59, 0, 0, loc)
	bot.now = func() time.Time { return now }
	c := bot.config()
	c.DailyReport, c.Alerts, c.AutoBackup, c.BackupTime = false, false, true, "03:00"
	if e := bot.configure(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	bot.tick(context.Background())
	if len(f.documents) != 0 {
		t.Fatal("early backup")
	}
	now = now.Add(time.Minute)
	f.fail = "sendDocument"
	bot.tick(context.Background())
	if bot.state.LastBackup != "" || bot.View().BackupError == "" {
		t.Fatal("failed backup marked delivered or error missing")
	}
	f.fail = ""
	bot.tick(context.Background())
	if len(f.documents) != 0 {
		t.Fatal("retry backoff ignored")
	}
	now = now.Add(time.Minute)
	bot.tick(context.Background())
	if len(f.documents) != 1 || bot.state.LastBackup != "2026-10-24" || bot.View().BackupError != "" {
		t.Fatal("backup did not retry successfully")
	}
	bot.tick(context.Background())
	if len(f.documents) != 1 {
		t.Fatal("duplicate daily backup")
	}
	reloaded, e := NewTelegram(bot.h, bot.file)
	if e != nil {
		t.Fatal(e)
	}
	reloaded.client, reloaded.baseURL, reloaded.sendInterval = bot.client, bot.baseURL, 0
	reloaded.now, reloaded.latest = bot.now, bot.latest
	reloaded.tick(context.Background())
	if len(f.documents) != 1 {
		t.Fatal("restart repeated delivered backup")
	}
	now = time.Date(2026, 10, 28, 2, 0, 0, 0, loc)
	reloaded.tick(context.Background())
	reloaded.tick(context.Background())
	if len(f.documents) != 2 || reloaded.state.LastBackup != "2026-10-27" {
		t.Fatal("missed backup not caught up once")
	}
}

func TestTelegramReportFailureDoesNotSuppressBackup(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	bot.now = func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) }
	c := bot.config()
	c.AutoBackup, c.Alerts = true, false
	if e := bot.configure(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	f.fail = "sendMessage"
	bot.tick(context.Background())
	if bot.state.LastReport != "" || bot.state.LastBackup != "2026-10-24" || len(f.documents) != 1 {
		t.Fatal("report failure suppressed automatic backup")
	}
}

func TestTelegramSchedulerSurvivesPollingFailureAndLongPoll(t *testing.T) {
	for _, mode := range []string{"conflict", "server_error", "long_poll"} {
		t.Run(mode, func(t *testing.T) {
			bot, f, _ := newTelegramTest(t)
			bot.now = func() time.Time { return time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) }
			c := bot.config()
			c.Alerts, c.AutoBackup = false, true
			if e := bot.configure(context.Background(), c); e != nil {
				t.Fatal(e)
			}
			pollStarted := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/getUpdates") {
					_, _ = io.Copy(io.Discard, r.Body)
					select {
					case pollStarted <- struct{}{}:
					default:
					}
					if mode == "long_poll" {
						<-r.Context().Done()
						return
					}
					code := 409
					if mode == "server_error" {
						code = 500
					}
					w.WriteHeader(code)
					fmt.Fprintf(w, `{"ok":false,"error_code":%d}`, code)
					return
				}
				f.serve(w, r)
			}))
			defer server.Close()
			bot.client, bot.baseURL = server.Client(), server.URL
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); bot.Run(ctx) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Error("worker failed to stop")
				}
			}()
			select {
			case <-pollStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("poll did not start")
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				f.mu.Lock()
				sent := len(f.documents) == 1 && len(f.texts) >= 1
				f.mu.Unlock()
				if sent {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("outgoing report/backup blocked by polling")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if mode != "long_poll" && bot.View().Error == "" {
				t.Fatal("outgoing success hid the polling failure")
			}
		})
	}
}

func installTelegramDownload(t *testing.T, bot *Telegram, f *telegramFake, payload []byte, filePath string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getFile") {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"file_path": filePath, "file_size": len(payload)}})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/file/") {
			_, _ = w.Write(payload)
			return
		}
		f.serve(w, r)
	}))
	t.Cleanup(server.Close)
	bot.client, bot.baseURL = server.Client(), server.URL
}

func TestTelegramRestoreUploadPreviewConfirmationAndCompletion(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(fmt.Sprint(encrypted), func(t *testing.T) {
			bot, f, _ := newTelegramTest(t)
			ops := &telegramJobOps{job: MaintenanceJob{ID: "restore-job", Kind: "restore", State: "running"}}
			bot.h.ops = ops
			account(t, bot.h.store, 100)
			raw := backupBytes(t, bot.h.store)
			payload := raw
			if encrypted {
				if e := bot.ensureBackupKey(); e != nil {
					t.Fatal(e)
				}
				var e error
				payload, e = sealTelegramBackup(raw, bot.config().BackupKey)
				if e != nil {
					t.Fatal(e)
				}
			}
			installTelegramDownload(t, bot, f, payload, "documents/restore.wdb")
			if e := bot.restorePrompt(context.Background()); e != nil {
				t.Fatal(e)
			}
			msg := replyToBot(bot, "")
			msg.Document = &telegramDocument{ID: "backup-id", Size: int64(len(payload))}
			if e := bot.message(context.Background(), msg); e != nil {
				t.Fatal(e)
			}
			if len(ops.requests) != 0 || bot.confirmation == nil || bot.confirmation.Summary.Accounts != 1 {
				t.Fatal("restore did not wait for validated preview confirmation")
			}
			key := bot.confirmation.Key
			if e := bot.execute(context.Background(), key); e != nil {
				t.Fatal(e)
			}
			if len(ops.requests) != 1 || !bytes.Equal(ops.requests[0].Backup, raw) || ops.requests[0].ExpectedZone != "soar.example.org" || len(ops.requests[0].SHA256) != 64 {
				t.Fatal("restore request did not preserve validated snapshot")
			}
			if e := bot.execute(context.Background(), key); e == nil || len(ops.requests) != 1 {
				t.Fatal("restore replay accepted")
			}
			ops.job.State = "succeeded"
			bot.cfg.DailyReport, bot.cfg.Alerts = false, false
			bot.tick(context.Background())
			if bot.state.PendingAction != "" {
				t.Fatal("restore completion not recovered")
			}
		})
	}
}

func TestTelegramRestoreRejectsInvalidUploadsAndOtherAdmins(t *testing.T) {
	for _, mode := range []string{"corrupt", "path_traversal", "oversized", "unprompted", "other_sender", "wrong_zone"} {
		t.Run(mode, func(t *testing.T) {
			bot, f, _ := newTelegramTest(t)
			ops := &telegramJobOps{}
			bot.h.ops = ops
			payload := backupBytes(t, bot.h.store)
			path := "documents/restore.db"
			if mode == "corrupt" {
				payload = []byte("not a database")
			}
			if mode == "path_traversal" {
				path = "../bot-secret/getMe"
			}
			if mode == "wrong_zone" {
				bot.h.inspectBackup = func(context.Context, string) (BackupSummary, error) {
					return BackupSummary{BaseZone: "other.example.org"}, nil
				}
			}
			installTelegramDownload(t, bot, f, payload, path)
			if e := bot.restorePrompt(context.Background()); e != nil {
				t.Fatal(e)
			}
			msg := replyToBot(bot, "")
			msg.Document = &telegramDocument{ID: "backup-id", Size: int64(len(payload))}
			if mode == "oversized" {
				msg.Document.Size = telegramFileLimit + 1
			}
			if mode == "unprompted" {
				msg.Reply = nil
			}
			if mode == "other_sender" {
				msg.From.ID = 99
				if e := bot.handleUpdate(context.Background(), telegramUpdate{ID: 1, Message: &msg}); e != nil {
					t.Fatal(e)
				}
			} else if e := bot.message(context.Background(), msg); e == nil {
				t.Fatal("invalid upload accepted")
			}
			if bot.confirmation != nil || len(ops.requests) != 0 {
				t.Fatal("invalid upload reached restore")
			}
		})
	}
}

func TestTelegramSettingsPreviewValidationConflictAndPreservation(t *testing.T) {
	bot, _, _ := newTelegramTest(t)
	ctx := context.Background()
	account := account(t, bot.h.store, 100)
	before, _ := bot.h.store.Get(account.ID)
	credential, _ := bot.h.store.SubscriptionToken(account.ID, false)
	if e := bot.callback(ctx, "server:server_stream_limit"); e != nil {
		t.Fatal(e)
	}
	if e := bot.message(ctx, replyToBot(bot, "-1")); e == nil {
		t.Fatal("invalid limit accepted")
	}
	if e := bot.message(ctx, replyToBot(bot, "۲۵۶")); e != nil {
		t.Fatal(e)
	}
	cfg, _ := bot.h.store.Settings()
	if cfg.ServerStreamLimit == 256 {
		t.Fatal("settings changed before confirmation")
	}
	if e := bot.execute(ctx, bot.confirmation.Key); e != nil {
		t.Fatal(e)
	}
	after, _ := bot.h.store.Get(account.ID)
	currentToken, _ := bot.h.store.SubscriptionToken(account.ID, false)
	if !reflect.DeepEqual(before, after) || credential != currentToken {
		t.Fatal("server setting changed account identity, counters or subscription")
	}
	if e := bot.callback(ctx, "server:country"); e != nil {
		t.Fatal(e)
	}
	if e := bot.message(ctx, replyToBot(bot, "GLOBAL")); e != nil {
		t.Fatal(e)
	}
	cfg, _ = bot.h.store.Settings()
	cfg.ServerStreamLimit = 64
	if e := bot.h.store.SaveSettings(cfg); e != nil {
		t.Fatal(e)
	}
	if e := bot.execute(ctx, bot.confirmation.Key); !errors.Is(e, ErrConflict) {
		t.Fatal("stale settings confirmation overwrote current settings", e)
	}
}

func TestTelegramPreferenceSchedulingAndStaleConfirmation(t *testing.T) {
	bot, _, _ := newTelegramTest(t)
	ctx := context.Background()
	for _, field := range []string{"report_time", "backup_time", "timezone", "backup_key"} {
		if e := bot.callback(ctx, "schedule:"+field); e != nil {
			t.Fatal(e)
		}
		if e := bot.message(ctx, replyToBot(bot, "invalid")); e == nil {
			t.Fatalf("invalid %s accepted", field)
		}
	}
	if e := bot.callback(ctx, "schedule:backup_time"); e != nil {
		t.Fatal(e)
	}
	if e := bot.message(ctx, replyToBot(bot, "۰۴:۳۰")); e != nil {
		t.Fatal(e)
	}
	if e := bot.execute(ctx, bot.confirmation.Key); e != nil || bot.config().BackupTime != "04:30" {
		t.Fatal("backup time not saved", e)
	}
	if e := bot.callback(ctx, "schedule:auto_backup"); e != nil {
		t.Fatal(e)
	}
	key := bot.confirmation.Key
	if e := bot.preference(ctx, "alerts"); e != nil {
		t.Fatal(e)
	}
	if e := bot.execute(ctx, key); !errors.Is(e, ErrConflict) {
		t.Fatal("stale preference confirmation accepted", e)
	}
	if e := bot.callback(ctx, "schedule:auto_backup"); e != nil {
		t.Fatal(e)
	}
	if e := bot.execute(ctx, bot.confirmation.Key); e != nil || !bot.config().AutoBackup || bot.state.BackupStarted == 0 {
		t.Fatal("automatic backup not initialized", e)
	}
}

func TestTelegramQuarantineReleaseRequiresCurrentConfirmation(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	ctx := context.Background()
	c := account(t, bot.h.store, 100)
	now := time.Now().UnixMilli()
	block := FloodBlock{At: now, Until: now + 300000, Strikes: 1, Reason: "target_scan"}
	if e := bot.h.store.saveFloodBlock(c.ID, &block); e != nil {
		t.Fatal(e)
	}
	g := bot.h.manager.flood
	g.mu.Lock()
	g.blocks[c.ID] = block
	g.mu.Unlock()
	if e := bot.user(ctx, c.ID); e != nil || !strings.Contains(f.texts[len(f.texts)-1], "target_scan") {
		t.Fatal("account view omitted quarantine", e)
	}
	if e := bot.callback(ctx, "release:"+c.ID); e != nil {
		t.Fatal(e)
	}
	if !g.blocked(c.ID) {
		t.Fatal("quarantine released without confirmation")
	}
	in := accountInput(c)
	in.Name = "Concurrent account change"
	if _, e := bot.h.store.Edit(c.ID, in); e != nil {
		t.Fatal(e)
	}
	if e := bot.execute(ctx, bot.confirmation.Key); !errors.Is(e, ErrConflict) || !g.blocked(c.ID) {
		t.Fatal("stale release accepted", e)
	}
	before, _ := bot.h.store.Get(c.ID)
	if e := bot.callback(ctx, "release:"+c.ID); e != nil {
		t.Fatal(e)
	}
	if e := bot.execute(ctx, bot.confirmation.Key); e != nil {
		t.Fatal(e)
	}
	after, _ := bot.h.store.Get(c.ID)
	if g.blocked(c.ID) || !reflect.DeepEqual(before, after) {
		t.Fatal("release failed or changed account data")
	}
	blocks, e := bot.h.store.floodBlocks()
	if e != nil || len(blocks) != 0 {
		t.Fatal("release not persisted", e)
	}
}

func TestTelegramManagementMenusAuthorizationAndTextBounds(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	ctx := context.Background()
	bot.h.ops = &telegramJobOps{}
	for _, command := range []string{"/settings", "/backup", "/restore", "/diagnostics", "/help", "/stats@whitedns_test_bot"} {
		if e := bot.message(ctx, *tgMessage(1, command).Message); e != nil {
			t.Fatal(command, e)
		}
	}
	for _, callback := range []string{"server", "diagnostics", "flood", "backups", "engines", "backup-now", "backup-key", "schedule:auto_backup"} {
		u := tgCallback(bot.state.Offset+1, callback)
		u.Callback.From.ID = 99
		before := len(f.texts)
		if e := bot.handleUpdate(ctx, u); e != nil {
			t.Fatal(e)
		}
		if len(f.texts) != before {
			t.Fatal("unauthorized sender reached management", callback)
		}
	}
	text := strings.Repeat("متن 🧪 ", 3000)
	parts := telegramTextParts(text)
	if strings.Join(parts, "") != text {
		t.Fatal("message splitting lost content")
	}
	for _, part := range parts {
		if len(utf16.Encode([]rune(part))) > 4096 {
			t.Fatal("Telegram text limit exceeded")
		}
	}
}
