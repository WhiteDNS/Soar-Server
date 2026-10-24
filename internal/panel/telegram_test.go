package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
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

const testTelegramToken = "123456789:abcdefghijklmnopqrstuvwxyz_123456789"

type telegramFake struct {
	mu         sync.Mutex
	texts      []string
	parseModes []string
	keyboards  []telegramKeyboard
	photos     int
	calls      map[string]int
	fail       string
	webhook    string
	nextID     int64
	updates    []telegramUpdate
}

func (f *telegramFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	f.calls[method]++
	if f.fail == method {
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"description":"secret token must never surface","parameters":{"retry_after":1}}`)
		return
	}
	var body struct {
		Text      string           `json:"text"`
		Markup    telegramKeyboard `json:"reply_markup"`
		Offset    int64            `json:"offset"`
		ParseMode string           `json:"parse_mode"`
	}
	if method != "sendPhoto" {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	var result any = true
	switch method {
	case "getMe":
		result = map[string]any{"id": 9988, "is_bot": true, "username": "whitedns_test_bot"}
	case "getChat":
		result = map[string]any{"id": int64(12345), "type": "private"}
	case "getWebhookInfo":
		result = map[string]string{"url": f.webhook}
	case "sendMessage":
		f.nextID++
		f.texts = append(f.texts, body.Text)
		f.parseModes = append(f.parseModes, body.ParseMode)
		f.keyboards = append(f.keyboards, body.Markup)
		result = map[string]any{"message_id": f.nextID}
	case "sendPhoto":
		f.photos++
		result = map[string]any{"message_id": 99}
	case "getUpdates":
		// Telegram retains updates until a later offset acknowledges them.
		pending := []telegramUpdate{}
		for _, update := range f.updates {
			if update.ID >= body.Offset {
				pending = append(pending, update)
			}
		}
		f.updates = pending
		result = pending
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}
func newTelegramTest(t *testing.T) (*Telegram, *telegramFake, *browser) {
	t.Helper()
	b, _, h := testBrowser(t)
	bot, e := NewTelegram(h, telegramPath(h.store))
	if e != nil {
		t.Fatal(e)
	}
	f := &telegramFake{calls: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	bot.baseURL = server.URL
	bot.client = server.Client()
	bot.sendInterval = 0
	bot.now = func() time.Time { return time.Date(2026, 10, 24, 4, 0, 0, 0, time.UTC) }
	bot.latest = func(context.Context) (PanelRelease, error) { return PanelRelease{}, errors.New("offline fixture") }
	c := defaultTelegramConfig()
	c.Token = testTelegramToken
	c.ChatID = 12345
	c.Enabled = true
	c.Origin = b.url
	if e = bot.configure(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	return bot, f, b
}
func tgMessage(id int64, text string) telegramUpdate {
	return telegramUpdate{ID: id, Message: &telegramMessage{From: telegramUser{ID: 12345, Language: "en"}, Chat: telegramChat{ID: 12345, Type: "private"}, Text: text}}
}
func tgCallback(id int64, data string) telegramUpdate {
	return telegramUpdate{ID: id, Callback: &telegramCallback{ID: fmt.Sprint(id), Data: data, From: telegramUser{ID: 12345}, Message: &telegramMessage{Chat: telegramChat{ID: 12345, Type: "private"}}}}
}
func mustHandle(t *testing.T, bot *Telegram, u telegramUpdate) {
	t.Helper()
	if e := bot.handleUpdate(context.Background(), u); e != nil {
		t.Fatal(e)
	}
}
func TestTelegramAPIAuthenticationSecretLifecycleAndBackups(t *testing.T) {
	bot, _, b := newTelegramTest(t)
	b.request("/api/telegram", "GET", nil, 401)
	b.request("/api/telegram", "PUT", map[string]bool{"enabled": false}, 401)
	b.login()
	csrf := b.csrf
	b.csrf = "bad"
	b.request("/api/telegram", "DELETE", nil, 403)
	b.csrf = csrf
	check := func(raw []byte) {
		t.Helper()
		if bytes.Contains(raw, []byte(testTelegramToken)) || bytes.Contains(raw, []byte(`"token"`)) {
			t.Fatal("token exposed in HTTP response")
		}
	}
	check(b.request("/api/telegram", "GET", nil, 200))
	check(b.request("/api/telegram/test", "POST", map[string]any{}, 200))
	check(b.request("/api/telegram", "PUT", map[string]any{"token": "", "enabled": true}, 200))
	if bot.config().Token != testTelegramToken {
		t.Fatal("blank token did not preserve token")
	}
	raw, e := os.ReadFile(bot.file)
	if e != nil || !bytes.Contains(raw, []byte(testTelegramToken)) {
		t.Fatal("token not saved privately", e)
	}
	info, _ := os.Stat(bot.file)
	if info.Mode().Perm() != 0600 {
		t.Fatal("unsafe config permissions")
	}
	if bytes.Contains(backupBytes(t, bot.h.store), []byte(testTelegramToken)) {
		t.Fatal("database backup contains Telegram token")
	}
	b.request("/api/telegram", "PUT", map[string]any{"enabled": false}, 200)
	if bot.config().Enabled {
		t.Fatal("bot remained enabled")
	}
	check(b.request("/api/telegram", "DELETE", nil, 200))
	if bot.config().Token != "" {
		t.Fatal("disconnect retained token")
	}
	disk, _ := os.ReadFile(bot.file)
	if bytes.Contains(disk, []byte(testTelegramToken)) {
		t.Fatal("disconnect retained on-disk token")
	}
	if _, e := NewTelegram(bot.h, bot.file); e != nil {
		t.Fatal(e)
	}
}
func TestTelegramRejectsOtherChatsSendersAndReplayedUpdates(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	cases := []telegramUpdate{tgMessage(1, "/start"), tgMessage(2, "/start"), tgMessage(3, "/start"), tgCallback(4, "new")}
	cases[0].Message.Chat.Type = "group"
	cases[1].Message.From.ID = 777
	cases[2].Message.Chat.ID = 777
	cases[3].Callback.From.ID = 777
	for _, u := range cases {
		mustHandle(t, bot, u)
	}
	if len(f.texts) != 0 || bot.conversation != nil {
		t.Fatal("unauthorized user reached bot")
	}
	mustHandle(t, bot, tgMessage(5, "/start"))
	n := len(f.texts)
	mustHandle(t, bot, tgMessage(5, "/start"))
	if len(f.texts) != n {
		t.Fatal("duplicate update dispatched")
	}
	reloaded, e := NewTelegram(bot.h, bot.file)
	if e != nil {
		t.Fatal(e)
	}
	if reloaded.state.Offset != 6 {
		t.Fatal("offset not durable")
	}
	reloaded.baseURL = bot.baseURL
	reloaded.client = bot.client
	mustHandle(t, reloaded, tgMessage(5, "/start"))
	if len(f.texts) != n {
		t.Fatal("restart replayed update")
	}
}
func TestTelegramGuidedCreationEditLimitsAndCancel(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	ctx := context.Background()
	mustHandle(t, bot, tgCallback(1, "new"))
	if bot.conversation.Field != "name" {
		t.Fatal("name prompt missing")
	}
	u := tgMessage(2, "Sample user")
	u.Message.Reply = &struct {
		ID int64 `json:"message_id"`
	}{bot.conversation.Prompt}
	mustHandle(t, bot, u)
	key := bot.conversation.Key
	mustHandle(t, bot, tgCallback(3, "set:"+key+":quota:5"))
	mustHandle(t, bot, tgCallback(4, "set:"+key+":expiry:30d"))
	for field, val := range map[string]string{"rate": "512", "streams": "64", "soar": "32", "cotten": "48", "note": "a note"} {
		if e := bot.setField(bot.conversation, field, val); e != nil {
			t.Fatal(e)
		}
	}
	mustHandle(t, bot, tgCallback(5, "save:"+key))
	nonce := bot.confirmation.Key
	mustHandle(t, bot, tgCallback(6, "ok:"+nonce))
	mustHandle(t, bot, tgCallback(7, "ok:"+nonce))
	list, _ := bot.h.store.List()
	if len(list) != 1 {
		t.Fatalf("create/replay count: %d", len(list))
	}
	c := list[0]
	if c.Name != "Sample user" || c.Engines != "both" || c.QuotaBytes != 5<<30 || c.RateBytes != 512*1024 || c.StreamLimit != 64 || c.SoarSessionLimit != 32 || c.CottenSessionLimit != 48 || c.ExpiresAt != bot.now().Add(30*24*time.Hour).UnixMilli() {
		t.Fatalf("wrong created user: %+v", c)
	}
	if e := bot.callback(ctx, "edit:"+c.ID); e != nil {
		t.Fatal(e)
	}
	if e := bot.setField(bot.conversation, "name", "Renamed"); e != nil {
		t.Fatal(e)
	}
	if e := bot.callback(ctx, "save:"+bot.conversation.Key); e != nil {
		t.Fatal(e)
	}
	if e := bot.execute(ctx, bot.confirmation.Key); e != nil {
		t.Fatal(e)
	}
	c, _ = bot.h.store.Get(c.ID)
	if c.Name != "Renamed" {
		t.Fatal("edit failed")
	}
	beforeLinks := len(f.texts)
	if e := bot.links(ctx, c.ID); e != nil {
		t.Fatal(e)
	}
	if f.photos != 0 {
		t.Fatal("Telegram sharing sent QR images")
	}
	profiles, e := bot.h.store.Profiles(c.ID)
	if e != nil {
		t.Fatal(e)
	}
	credential, e := bot.h.store.SubscriptionToken(c.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	wantLinks := []string{bot.config().Origin + "/sub/" + credential, profiles["soar"], profiles["cotten"]}
	shared := f.texts[beforeLinks:]
	if len(shared) != len(wantLinks) {
		t.Fatalf("wanted one message per connection string, got %d", len(shared))
	}
	for i, link := range wantLinks {
		if f.parseModes[beforeLinks+i] != "HTML" || !strings.HasSuffix(shared[i], "\n<pre>"+html.EscapeString(link)+"</pre>") {
			t.Fatalf("connection string is not a copyable code block: %q", shared[i])
		}
	}
	if e := bot.callback(ctx, "new"); e != nil {
		t.Fatal(e)
	}
	if e := bot.message(ctx, *tgMessage(8, "/cancel").Message); e != nil {
		t.Fatal(e)
	}
	if bot.conversation != nil {
		t.Fatal("cancel retained draft")
	}
}
func TestTelegramConfirmationsRejectStaleReusedAndExpiredMutations(t *testing.T) {
	for _, action := range []string{"delete", "reset", "rotate", "subscription-rotate", "toggle", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			bot, _, _ := newTelegramTest(t)
			c := account(t, bot.h.store, 1000)
			ctx := context.Background()
			if e := bot.callback(ctx, "action:"+action+":"+c.ID); e != nil {
				t.Fatal(e)
			}
			key := bot.confirmation.Key
			in := accountInput(c)
			in.Name = "Concurrent dashboard edit"
			if _, e := bot.h.store.Edit(c.ID, in); e != nil {
				t.Fatal(e)
			}
			if e := bot.execute(ctx, key); !errors.Is(e, ErrConflict) {
				t.Fatalf("stale %s accepted: %v", action, e)
			}
			if e := bot.execute(ctx, key); e == nil {
				t.Fatal("reused confirmation accepted")
			}
			if e := bot.callback(ctx, "action:"+action+":"+c.ID); e != nil {
				t.Fatal(e)
			}
			bot.confirmation.Expires = bot.now()
			if e := bot.execute(ctx, bot.confirmation.Key); e == nil {
				t.Fatal("expired confirmation accepted")
			}
		})
	}
}
func TestTelegramAllPerUserActions(t *testing.T) {
	bot, _, _ := newTelegramTest(t)
	ctx := context.Background()
	c := account(t, bot.h.store, 1000)
	_, _, _ = bot.h.store.reserve(c.ID, 10, true)
	act := func(action string) {
		t.Helper()
		if e := bot.callback(ctx, "action:"+action+":"+c.ID); e != nil {
			t.Fatal(e)
		}
		if e := bot.execute(ctx, bot.confirmation.Key); e != nil {
			t.Fatal(e)
		}
	}
	act("toggle")
	c, _ = bot.h.store.Get(c.ID)
	if c.Enabled {
		t.Fatal("toggle failed")
	}
	act("toggle")
	act("reset")
	c, _ = bot.h.store.Get(c.ID)
	if c.Used() != 0 {
		t.Fatal("usage reset failed")
	}
	oldKey := c.PublicKey
	act("rotate")
	c, _ = bot.h.store.Get(c.ID)
	if oldKey == c.PublicKey {
		t.Fatal("key not rotated")
	}
	credential, _ := bot.h.store.SubscriptionToken(c.ID, false)
	act("subscription-rotate")
	newCredential, _ := bot.h.store.SubscriptionToken(c.ID, false)
	if credential == newCredential {
		t.Fatal("subscription URL not rotated")
	}
	act("disconnect")
	act("delete")
	if _, e := bot.h.store.Get(c.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("delete failed")
	}
}
func TestTelegramValidationLanguagesAndPrivacy(t *testing.T) {
	bot, f, b := newTelegramTest(t)
	for _, patch := range []map[string]any{{"chat_id": -123}, {"language": "xx"}, {"report_time": "9:00"}, {"timezone": "unknown/zone"}, {"token": "invalid"}} {
		b.login()
		b.request("/api/telegram", "PUT", patch, 400)
	}
	f.webhook = "https://other.example/hook"
	b.request("/api/telegram", "PUT", map[string]any{"enabled": true}, 400)
	f.webhook = ""
	c := bot.config()
	c.Language = "auto"
	bot.mu.Lock()
	bot.cfg = c
	bot.mu.Unlock()
	u := tgMessage(1, "/start")
	u.Message.From.Language = "fa-IR"
	mustHandle(t, bot, u)
	if bot.config().Language != "fa" || !strings.Contains(f.texts[len(f.texts)-1], "مدیریت") {
		t.Fatal("Persian language detection failed")
	}
	if e := bot.preference(context.Background(), "language"); e != nil {
		t.Fatal(e)
	}
	if bot.config().Language != "en" {
		t.Fatal("language switch failed")
	}
	for field, value := range map[string]string{"quota": "NaN", "rate": "Inf", "soar": "0", "cotten": "65536", "streams": "-1", "expiry": "yesterday", "engines": "unknown"} {
		if e := bot.setField(&telegramConversation{}, field, value); e == nil {
			t.Fatalf("accepted %s %s", field, value)
		}
	}
	d := &telegramConversation{}
	if e := bot.setField(d, "quota", "۵"); e != nil || d.Input.QuotaBytes != 5<<30 {
		t.Fatal("Persian digits failed", e)
	}
	f.fail = "getMe"
	e := bot.call(context.Background(), c, "getMe", map[string]any{}, nil)
	if e == nil || strings.Contains(e.Error(), testTelegramToken) || strings.Contains(e.Error(), "secret") {
		t.Fatal("unsafe Telegram error", e)
	}
	var apiErr *telegramAPIError
	if !errors.As(e, &apiErr) || apiErr.Retry != 1 {
		t.Fatal("retry_after ignored")
	}
}
func TestTelegramNotificationsTransitionsPersistenceAndDailySchedule(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	c := account(t, bot.h.store, 100)
	ctx := context.Background()
	bot.tick(ctx)
	baseline := len(f.texts)
	_, _, _ = bot.h.store.reserve(c.ID, 100, true)
	bot.tick(ctx)
	if len(f.texts) != baseline+1 || !strings.Contains(f.texts[len(f.texts)-1], "exhausted") {
		t.Fatal("quota alert missing", f.texts)
	}
	bot.tick(ctx)
	if len(f.texts) != baseline+1 {
		t.Fatal("repeated quota alert")
	}
	_ = bot.h.store.ResetUsage(c.ID)
	bot.tick(ctx)
	_, _, _ = bot.h.store.reserve(c.ID, 100, true)
	bot.tick(ctx)
	if len(f.texts) != baseline+2 {
		t.Fatal("quota re-crossing missed")
	}
	c, _ = bot.h.store.Get(c.ID)
	in := accountInput(c)
	in.ExpiresAt = bot.now().Add(-time.Hour).UnixMilli()
	_, _ = bot.h.store.Edit(c.ID, in)
	bot.tick(ctx)
	if !strings.Contains(f.texts[len(f.texts)-1], "expired") {
		t.Fatal("expiry alert missing")
	}
	_ = bot.h.store.Delete(c.ID)
	bot.tick(ctx)
	if len(bot.state.Accounts) != 0 {
		t.Fatal("deleted account retained")
	}
	config := bot.config()
	loc, _ := time.LoadLocation("Asia/Tehran")
	before := time.Date(2026, 10, 24, 8, 59, 0, 0, loc)
	after := before.Add(time.Minute)
	if _, due := telegramReportDue(before, config, "", 0); due {
		t.Fatal("early report")
	}
	if day, due := telegramReportDue(after, config, "", 0); !due || day != "2026-10-24" {
		t.Fatal("09:00 Tehran report missing")
	}
	if _, due := telegramReportDue(after, config, "2026-10-24", 0); due {
		t.Fatal("duplicate daily report")
	}
	if day, due := telegramReportDue(before, config, "2026-10-20", 0); !due || day != "2026-10-23" {
		t.Fatal("missed report catchup incorrect")
	}
	bot.now = func() time.Time { return after }
	f.fail = "sendMessage"
	bot.tick(ctx)
	if bot.state.LastReport != "" {
		t.Fatal("failed report marked delivered")
	}
	f.fail = ""
	bot.tick(ctx)
	if bot.state.LastReport != "2026-10-24" {
		t.Fatal("report retry failed")
	}
	n := len(f.texts)
	bot.tick(ctx)
	if len(f.texts) != n {
		t.Fatal("report repeated")
	}
	reloaded, e := NewTelegram(bot.h, bot.file)
	if e != nil || reloaded.state.LastReport != "2026-10-24" {
		t.Fatal("delivery state not durable", e)
	}
}
func TestTelegramRestartCompletionAndInterruptedActionRecovery(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	ctx := context.Background()
	ops := &telegramJobOps{job: MaintenanceJob{ID: "job", State: "running", Kind: "restart"}}
	bot.h.ops = ops
	if e := bot.callback(ctx, "restart"); e != nil {
		t.Fatal(e)
	}
	key := bot.confirmation.Key
	if e := bot.execute(ctx, key); e != nil {
		t.Fatal(e)
	}
	if len(ops.requests) != 1 || ops.requests[0].Action != "restart" {
		t.Fatal("restart not dispatched")
	}
	ops.job.State = "succeeded"
	ops.job.Message = "Panel restarted"
	bot.tick(ctx)
	if bot.state.PendingAction != "" {
		t.Fatal("completed job not cleared")
	}
	found := false
	for _, text := range f.texts {
		found = found || strings.Contains(text, "succeeded")
	}
	if !found {
		t.Fatal("result not delivered")
	}
	bot.state.PendingAction = "create"
	_ = bot.save()
	reloaded, e := NewTelegram(bot.h, bot.file)
	if e != nil {
		t.Fatal(e)
	}
	reloaded.client = bot.client
	reloaded.baseURL = bot.baseURL
	reloaded.now = bot.now
	reloaded.latest = bot.latest
	reloaded.tick(ctx)
	if reloaded.state.PendingAction != "" {
		t.Fatal("ambiguous action not cleared")
	}
	if list, _ := bot.h.store.List(); len(list) != 0 {
		t.Fatal("interrupted action replayed")
	}
}

type telegramJobOps struct {
	job      MaintenanceJob
	requests []MaintenanceRequest
}

func (f *telegramJobOps) Status(context.Context) (MaintenanceJob, error) { return f.job, nil }
func (f *telegramJobOps) Start(_ context.Context, r MaintenanceRequest) (MaintenanceJob, error) {
	f.requests = append(f.requests, r)
	return f.job, nil
}
func (f *telegramJobOps) AllowHTTPS(context.Context) error { return nil }
func TestTelegramPersistenceFailurePreventsMutation(t *testing.T) {
	bot, _, _ := newTelegramTest(t)
	c := account(t, bot.h.store, 1)
	if e := bot.callback(context.Background(), "action:delete:"+c.ID); e != nil {
		t.Fatal(e)
	}
	block := filepath.Join(t.TempDir(), "not-a-directory")
	_ = os.WriteFile(block, []byte("x"), 0600)
	bot.file = filepath.Join(block, "telegram.json")
	if e := bot.execute(context.Background(), bot.confirmation.Key); e == nil {
		t.Fatal("write failure ignored")
	}
	if _, e := bot.h.store.Get(c.ID); e != nil {
		t.Fatal("mutation happened without marker")
	}
}

func TestTelegramSearchPaginationFormsAndCallbackSizes(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_, e := bot.h.store.Create(ClientInput{Name: fmt.Sprintf("Customer %02d", i), Enabled: false})
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := bot.users(ctx, 0, ""); e != nil {
		t.Fatal(e)
	}
	if len(f.keyboards[len(f.keyboards)-1].Rows) != 10 {
		t.Fatal("pagination missing")
	}
	if e := bot.users(ctx, 0, "Customer 03"); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(f.texts[len(f.texts)-1], "Users: 1") {
		t.Fatal("search not filtered")
	}
	if e := bot.callback(ctx, "new"); e != nil {
		t.Fatal(e)
	}
	oldKey := bot.conversation.Key
	mustHandle(t, bot, tgMessage(1, "unrelated text"))
	if bot.conversation.Input.Name != "" {
		t.Fatal("unrelated message filled draft")
	}
	mustHandle(t, bot, tgMessage(2, "/cancel"))
	if e := bot.callback(ctx, "set:"+oldKey+":quota:10"); e == nil {
		t.Fatal("cancelled form buttons accepted")
	}
	if e := bot.callback(ctx, "new"); e != nil {
		t.Fatal(e)
	}
	bot.conversation.Expires = bot.now().Add(-time.Second)
	mustHandle(t, bot, tgCallback(3, "field:"+bot.conversation.Key+":name"))
	if bot.conversation != nil {
		t.Fatal("expired draft retained")
	}
	list, _ := bot.h.store.List()
	if e := bot.user(ctx, list[0].ID); e != nil {
		t.Fatal(e)
	}
	for _, k := range f.keyboards {
		for _, row := range k.Rows {
			for _, button := range row {
				if len(button.Data) > 64 {
					t.Fatalf("callback too long: %s", button.Data)
				}
			}
		}
	}
}
func TestTelegramStableUpdateChecksAndReleaseAlerts(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	ctx := context.Background()
	release := releaseFixture("99.0.0", false)
	bot.latest = func(context.Context) (PanelRelease, error) { return release, nil }
	bot.releaseVersion = func(context.Context, string) (PanelRelease, error) { return release, nil }
	ops := &telegramJobOps{job: MaintenanceJob{ID: "update-job", Kind: "update", State: "running"}}
	bot.h.ops = ops
	if e := bot.updates(ctx); e != nil {
		t.Fatal(e)
	}
	if len(ops.requests) != 0 {
		t.Fatal("update ran without confirmation")
	}
	if e := bot.execute(ctx, bot.confirmation.Key); e != nil {
		t.Fatal(e)
	}
	if len(ops.requests) != 1 || ops.requests[0].Version != "99.0.0" {
		t.Fatal("wrong update request")
	}
	if e := bot.callback(ctx, "restart"); e != nil {
		t.Fatal(e)
	}
	if e := bot.execute(ctx, bot.confirmation.Key); e == nil || len(ops.requests) != 1 {
		t.Fatal("pending job marker overwritten")
	}
	ops.job.State = "rolled_back"
	ops.job.Message = "Previous version restored"
	bot.tick(ctx)
	if bot.state.PendingAction != "" {
		t.Fatal("rollback result retained")
	}
	bot.state.LastReleaseCheck = 0
	bot.state.LastRelease = ""
	before := len(f.texts)
	bot.tick(ctx)
	if len(f.texts) != before+1 {
		t.Fatal("stable release alert missing")
	}
	bot.tick(ctx)
	if len(f.texts) != before+1 {
		t.Fatal("release alert repeated")
	}
	release = releaseFixture("99.1.0-beta1", true)
	if e := bot.updates(ctx); e == nil {
		t.Fatal("beta update offered")
	}
	release = releaseFixture("99.1.0", false)
	if e := bot.updates(ctx); e != nil {
		t.Fatal(e)
	}
	release.Prerelease = true
	if e := bot.execute(ctx, bot.confirmation.Key); e == nil || len(ops.requests) != 1 {
		t.Fatal("changed release bypassed revalidation")
	}
}
func TestTelegramRuntimeAlertsRespectLazyCottenAndRecovery(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	ctx := context.Background()
	_, e := bot.h.store.Create(ClientInput{Name: "Lazy Cotten", Engines: "cotten", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	bot.tick(ctx)
	before := len(f.texts)
	bot.h.manager.diagnostics.cottenStartErrors.Add(1)
	bot.tick(ctx)
	if len(f.texts) != before+1 || !strings.Contains(f.texts[len(f.texts)-1], "failure") {
		t.Fatal("runtime failure alert missing")
	}
	bot.tick(ctx)
	if len(f.texts) != before+1 {
		t.Fatal("repeated runtime alert")
	}
	bot.h.manager.diagnostics.cottenStarts.Add(1)
	bot.tick(ctx)
	if len(f.texts) != before+2 || !strings.Contains(f.texts[len(f.texts)-1], "recovered") {
		t.Fatal("runtime recovery missing")
	}
	// A later unused Cotten account must not resurrect an old counter error.
	_, _ = bot.h.store.Create(ClientInput{Name: "Unused Cotten", Engines: "cotten", Enabled: true})
	bot.tick(ctx)
	if len(f.texts) != before+2 {
		t.Fatal("lazy engine reported down")
	}
}
func TestTelegramWorkerReconfigurationAndCancellation(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	bot.cfg.Alerts = false
	bot.cfg.DailyReport = false
	f.mu.Lock()
	f.updates = []telegramUpdate{tgMessage(1, "/start")}
	f.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); bot.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		seen := len(f.texts) > 0
		f.mu.Unlock()
		if seen {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("worker did not dispatch")
		}
		time.Sleep(5 * time.Millisecond)
	}
	config := bot.config()
	config.Enabled = false
	if e := bot.configure(context.Background(), config); e != nil {
		cancel()
		t.Fatal(e)
	}
	if bot.View().Enabled {
		cancel()
		t.Fatal("disable failed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestTelegramStatsCommandReturnsCurrentServerSnapshot(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	c := account(t, bot.h.store, 1000)
	_, _, _ = bot.h.store.reserve(c.ID, 100, true)
	mustHandle(t, bot, tgMessage(1, "/stats"))
	text := f.texts[len(f.texts)-1]
	for _, want := range []string{"current state snapshot", "Version: " + Version, "Uptime:", "Accounts: 1", "Active TCP streams:", "CPU:", "Memory used/total", "Disk used/total", "not daily traffic", c.Name} {
		if !strings.Contains(text, want) {
			t.Fatalf("stats missing %q: %s", want, text)
		}
	}
	before := len(f.texts)
	u := tgMessage(2, "/stats")
	u.Message.From.ID = 777
	mustHandle(t, bot, u)
	if len(f.texts) != before {
		t.Fatal("unauthorized stats disclosure")
	}
	if e := bot.preference(context.Background(), "language"); e != nil {
		t.Fatal(e)
	}
	mustHandle(t, bot, tgMessage(3, "/stats"))
	if !strings.Contains(f.texts[len(f.texts)-1], "گزارش وضعیت فعلی") {
		t.Fatal("Persian stats missing")
	}
	current, _ := bot.h.store.Get(c.ID)
	if current.Used() != 100 {
		t.Fatal("stats changed usage")
	}
}

func TestTelegramTokenOnlySetupRequiresOneTimePrivatePairing(t *testing.T) {
	bot, f, b := newTelegramTest(t)
	b.login()
	b.request("/api/telegram", "DELETE", nil, 200)
	var view TelegramView
	if e := json.Unmarshal(b.request("/api/telegram", "PUT", map[string]any{"token": testTelegramToken, "enabled": true}, 200), &view); e != nil {
		t.Fatal(e)
	}
	if view.Paired || view.ChatID != 0 || view.Status != "awaiting_pairing" || !strings.HasPrefix(view.PairURL, "https://t.me/whitedns_test_bot?start=") {
		t.Fatalf("token-only setup not awaiting pairing: %+v", view)
	}
	b.request("/api/telegram/test", "POST", map[string]any{}, 400)
	nonce := bot.config().PairToken
	if len(nonce) != 43 || view.PairExpires != bot.now().Add(5*time.Minute).UnixMilli() {
		t.Fatal("pairing expiry/token invalid")
	}
	before := len(f.texts)
	mustHandle(t, bot, tgMessage(1, "/start"))
	mustHandle(t, bot, tgMessage(2, "/start wrong-secret"))
	u := tgMessage(3, "/start "+nonce)
	u.Message.Chat.Type = "group"
	mustHandle(t, bot, u)
	u = tgMessage(4, "/start "+nonce)
	u.Message.From.ID = 777
	mustHandle(t, bot, u)
	if len(f.texts) != before || bot.config().ChatID != 0 {
		t.Fatal("unknown sender claimed bot")
	}
	u = tgMessage(5, "/start "+nonce)
	u.Message.From.Language = "fa"
	mustHandle(t, bot, u)
	if bot.config().ChatID != 12345 || bot.config().PairToken != "" || !bot.View().Paired || bot.View().PairURL != "" {
		t.Fatal("pairing failed or nonce retained")
	}
	if bot.state.ReportStarted != bot.now().UnixMilli() {
		t.Fatal("pairing did not establish the first report baseline")
	}
	if !strings.Contains(f.texts[len(f.texts)-1], "متصل شد") {
		t.Fatal("pairing did not use Telegram language")
	}
	u = tgMessage(6, "/start "+nonce)
	u.Message.From.ID = 888
	u.Message.Chat.ID = 888
	mustHandle(t, bot, u)
	if bot.config().ChatID != 12345 {
		t.Fatal("used connection link rebound administrator")
	}
	b.request("/api/telegram/pair", "POST", map[string]any{}, 400)
	reloaded, e := NewTelegram(bot.h, bot.file)
	if e != nil || reloaded.config().ChatID != 12345 || reloaded.config().PairToken != "" {
		t.Fatal("pairing not consumed durably", e)
	}
	if bytes.Contains(backupBytes(t, bot.h.store), []byte(nonce)) {
		t.Fatal("pairing secret entered database")
	}
}
func TestTelegramPairingExpiryRenewalAndTokenReplacement(t *testing.T) {
	bot, f, b := newTelegramTest(t)
	b.login()
	b.request("/api/telegram", "DELETE", nil, 200)
	b.request("/api/telegram", "PUT", map[string]any{"token": testTelegramToken, "enabled": true}, 200)
	old := bot.config().PairToken
	bot.now = func() time.Time { return time.Date(2026, 10, 24, 4, 6, 0, 0, time.UTC) }
	mustHandle(t, bot, tgMessage(1, "/start "+old))
	if bot.View().PairURL != "" || bot.View().Paired {
		t.Fatal("expired link authorized")
	}
	csrf := b.csrf
	b.csrf = "bad"
	b.request("/api/telegram/pair", "POST", map[string]any{}, 403)
	b.csrf = csrf
	b.request("/api/telegram/pair", "POST", map[string]any{}, 200)
	fresh := bot.config().PairToken
	if fresh == old {
		t.Fatal("renewal reused link")
	}
	mustHandle(t, bot, tgMessage(2, "/start "+old))
	if bot.View().Paired {
		t.Fatal("replaced link accepted")
	}
	// Losing the state file write must not grant even temporary in-memory access.
	file := bot.file
	block := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(block, []byte("x"), 0600)
	bot.file = filepath.Join(block, "telegram.json")
	if e := bot.handleUpdate(context.Background(), tgMessage(3, "/start "+fresh)); e == nil || bot.View().Paired {
		t.Fatal("pairing granted without durable state")
	}
	bot.file = file
	mustHandle(t, bot, tgMessage(4, "/start "+fresh))
	if !bot.View().Paired {
		t.Fatal("fresh connection link rejected")
	}
	replacement := "987654321:abcdefghijklmnopqrstuvwxyz_123456789"
	b.request("/api/telegram", "PUT", map[string]any{"token": replacement, "enabled": true}, 200)
	if bot.View().Paired || bot.config().ChatID != 0 || bot.View().PairURL == "" {
		t.Fatal("replacement token retained administrator")
	}
	before := len(f.texts)
	mustHandle(t, bot, tgMessage(5, "/stats"))
	if len(f.texts) != before {
		t.Fatal("old admin authorized before fresh pairing")
	}
}

func TestTelegramWorkerRecoversFromStatePersistenceFailure(t *testing.T) {
	bot, f, _ := newTelegramTest(t)
	bot.cfg.Alerts = false
	bot.cfg.DailyReport = false
	if e := os.Remove(bot.file); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(bot.file, 0700); e != nil {
		t.Fatal(e)
	}
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
	deadline := time.Now().Add(3 * time.Second)
	for bot.View().Status != "error" {
		if time.Now().After(deadline) {
			t.Fatal("state write failure not surfaced")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e := os.Remove(bot.file); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.updates = []telegramUpdate{tgMessage(1, "/stats")}
	f.mu.Unlock()
	if e := bot.configure(ctx, bot.config()); e != nil {
		t.Fatal(e)
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		f.mu.Lock()
		seen := len(f.texts) > 0
		f.mu.Unlock()
		if seen {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not recover after storage repair")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
