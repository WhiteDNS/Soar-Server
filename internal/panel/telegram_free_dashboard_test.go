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
	"testing"
	"time"
)

func freePublic(u telegramUpdate, id int64) telegramUpdate {
	if u.Message != nil {
		u.Message.From.ID, u.Message.Chat.ID = id, id
	} else {
		u.Callback.From.ID, u.Callback.Message.Chat.ID = id, id
	}
	return u
}

func freeReply(m telegramUpdate, prompt int64) telegramUpdate {
	m.Message.Reply = &struct {
		ID int64 `json:"message_id"`
	}{prompt}
	return m
}

func TestTelegramFreeAdminPublishesPlanAndDeliversAllEngineProfiles(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	now := time.Now()
	bot.now = func() time.Time { return now }
	var updateID int64
	step := func(data string) telegramUpdate {
		updateID++
		return tgCallback(updateID, data)
	}
	message := func(text string) telegramUpdate {
		updateID++
		return tgMessage(updateID, text)
	}
	tap := func(data string) { mustHandle(t, bot, step(data)) }
	tap("free:enable")
	tap("free:plannew")
	d := bot.conversation
	if d == nil || d.FreePlan == nil || d.MenuID == 0 {
		t.Fatal("no plan editor was displayed")
	}
	prefix := "free:editor:" + d.Key + ":"
	tap(prefix + "engines")
	tap(prefix + "engine:soar")
	tap(prefix + "engine:cotten")
	for _, field := range []struct{ key, value string }{{"name", "All engines"}, {"quota", "۲۰۴۸"}, {"days", "۹"}} {
		tap(prefix + field.key)
		if d.Prompt == 0 {
			t.Fatalf("no prompt for %s", field.key)
		}
		mustHandle(t, bot, freeReply(message(field.value), d.Prompt))
	}
	tap(prefix + "save")
	c, err := bot.h.store.freeCatalog()
	if err != nil || len(c.Plans) != 2 || c.Plans[1].Published {
		t.Fatalf("plan draft not saved: %+v, %v", c, err)
	}
	p := c.Plans[1]
	if p.Input.Name != "All engines" || p.Input.Engines != "all" || p.Input.QuotaBytes != 2<<30 || p.Days != 9 {
		t.Fatalf("plan fields were not saved: %+v", p)
	}
	tap("free:planpublish:" + p.ID + ":1")
	mustHandle(t, bot, freePublic(message("/free"), 888))
	rows := fake.keyboards[len(fake.keyboards)-1].Rows
	if len(rows) != 3 || rows[2][0].Data != "free:home" {
		t.Fatalf("public plan list has unexpected buttons: %+v", rows)
	}
	now = now.Add(4 * time.Second)
	mustHandle(t, bot, freePublic(step(rows[1][0].Data), 888))
	challenge := bot.freeChallenges[888]
	if challenge.Plan.ID != p.ID {
		t.Fatal("wrong plan selected")
	}
	now = now.Add(4 * time.Second)
	reply := freeReply(message(challenge.Code+" I need a connection for my laptop."), challenge.Prompt)
	mustHandle(t, bot, freePublic(reply, 888))
	_, req, err := bot.h.store.freeQueue(now)
	if err != nil || req.Key == "" {
		t.Fatalf("no pending request: %v", err)
	}
	tap("free:requestdetail:" + req.Key)
	approve := fake.keyboards[len(fake.keyboards)-1].Rows[0][0].Data
	tap(approve)
	owned, err := bot.h.store.freeRequest(888, now)
	if err != nil || owned.AccountID == "" {
		t.Fatalf("no account issued: %v", err)
	}
	links, err := bot.h.store.Profiles(owned.AccountID)
	if err != nil || len(links) != 3 {
		t.Fatalf("expected three profiles: %d, %v", len(links), err)
	}
	for _, engine := range profileEngines {
		now = now.Add(4 * time.Second)
		mustHandle(t, bot, freePublic(step("free:link:"+engine), 888))
		if fake.texts[len(fake.texts)-1] != links[engine] || fake.chatIDs[len(fake.chatIDs)-1] != 888 {
			t.Fatalf("wrong profile or recipient for %s", engine)
		}
		now = now.Add(4 * time.Second)
		mustHandle(t, bot, freePublic(step("free:qr:"+engine), 888))
	}
	if fake.photos != 3 {
		t.Fatalf("expected 3 QR photos, received %d", fake.photos)
	}
	tap("free:pause")
	now = now.Add(4 * time.Second)
	mustHandle(t, bot, freePublic(step("free:link:soar"), 888))
	if fake.texts[len(fake.texts)-1] != links["soar"] {
		t.Fatal("pausing admissions also stopped existing owners")
	}
	tap("free:disable")
	now = now.Add(4 * time.Second)
	sends := len(fake.texts)
	mustHandle(t, bot, freePublic(step("free:link:soar"), 888))
	if len(fake.texts) != sends {
		t.Fatal("emergency stop still served a public profile")
	}
}

func TestTelegramFreeReviewRefreshesTheOpenQueue(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	store, now := bot.h.store, bot.now()
	mustHandle(t, bot, tgCallback(1, "free:enable"))
	for _, id := range []int64{888, 999} {
		if err := store.submitFree(id, token()[:12], "I need access for this device.", now); err != nil {
			t.Fatal(err)
		}
	}
	list, err := store.freePending(now)
	if err != nil || len(list) != 2 {
		t.Fatalf("expected two requests: %v, %v", list, err)
	}
	next := int64(2)
	tap := func(data string) {
		u := tgCallback(next, data)
		next++
		u.Callback.Message.ID = 42
		edits, sends := fake.calls["editMessageText"], fake.calls["sendMessage"]
		mustHandle(t, bot, u)
		if fake.calls["editMessageText"] != edits+1 || fake.calls["sendMessage"] != sends {
			t.Fatal("review must edit the current message exactly once")
		}
	}
	detail := func(key string) string { return "free:requestdetail:" + key }
	tap("free:queue")
	tap(detail(list[0].Key))
	tap("free:approve:" + list[0].Key + "@" + bot.freeEpoch)
	assertFreeReviewQueue(t, fake, "Approved.", list[1].Key)
	tap(detail(list[0].Key))
	assertFreeReviewQueue(t, fake, "already reviewed", list[1].Key)
	tap(detail(list[1].Key))
	tap("free:reject:" + list[1].Key + "@" + bot.freeEpoch)
	assertFreeReviewQueue(t, fake, "Declined.")
}

func assertFreeReviewQueue(t *testing.T, fake *telegramFake, result string, expected ...string) {
	t.Helper()
	text := fake.texts[len(fake.texts)-1]
	if !strings.Contains(text, result) {
		t.Fatalf("review result missing from queue: %q", text)
	}
	var requests []string
	for _, row := range fake.keyboards[len(fake.keyboards)-1].Rows {
		if key, ok := strings.CutPrefix(row[0].Data, "free:requestdetail:"); ok {
			requests = append(requests, key)
		}
	}
	if strings.Join(requests, ",") != strings.Join(expected, ",") ||
		len(expected) == 0 && !strings.Contains(text, "No pending requests.") {
		t.Fatalf("stale queue after review: %v; want %v. Message: %q", requests, expected, text)
	}
}

func TestTelegramFreeReviewRefreshDoesNotWaitForRuntime(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	mustHandle(t, bot, tgCallback(1, "free:enable"))
	store, now := bot.h.store, bot.now()
	if err := store.submitFree(888, token()[:12], "I need access for this device.", now); err != nil {
		t.Fatal(err)
	}
	_, req, err := store.freeQueue(now)
	if err != nil || req.Key == "" {
		t.Fatalf("request was not queued: %v", err)
	}
	bot.h.manager.reconcileMu.Lock()
	done := make(chan error, 1)
	defer func() {
		bot.h.manager.reconcileMu.Unlock()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	approve := tgCallback(2, "free:approve:"+req.Key+"@"+bot.freeEpoch)
	approve.Callback.Message.ID = 42
	go func() { done <- bot.handleUpdate(context.Background(), approve) }()

	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		fake.mu.Lock()
		updated := fake.calls["editMessageText"] == 1 && strings.Contains(fake.texts[len(fake.texts)-1], "No pending requests.")
		fake.mu.Unlock()
		if updated {
			return
		}
	}
	t.Fatal("the queue was not refreshed while runtime preparation was blocked")
}

func TestTelegramFreeBulkReviewRespectsCapacityAndFrozenSelection(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	s, now := bot.h.store, bot.now()
	for id := int64(1); id < freeDailyLimit; id++ {
		if e := s.submitFree(id, token()[:12], "Request for test account", now); e != nil {
			t.Fatal(e)
		}
		_, first, _ := s.freeQueue(now)
		if _, e := s.approveFree(first.Key, now); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []int64{21, 22} {
		if e := s.submitFree(id, token()[:12], "Request for test account", now); e != nil {
			t.Fatal(e)
		}
	}
	mustHandle(t, bot, tgCallback(1, "free:enable"))
	mustHandle(t, bot, tgCallback(2, "free:selectall"))
	mustHandle(t, bot, tgCallback(3, "free:bulkapprove"))
	if bot.confirmation == nil || len(bot.confirmation.FreeIDs) != 2 {
		t.Fatal("bulk review did not pin its selection")
	}
	if e := s.submitFree(23, token()[:12], "Request for later account", now); e != nil {
		t.Fatal(e)
	}
	mustHandle(t, bot, tgCallback(4, "ok:"+bot.confirmation.Key))
	left, e := s.freePending(now)
	if e != nil || len(left) != 2 || !strings.HasPrefix(left[0].Key, "22:") || !strings.HasPrefix(left[1].Key, "23:") {
		t.Fatalf("incorrect partial review: %+v, %v", left, e)
	}
	assertFreeReviewQueue(t, fake, "Still pending.", left[0].Key, left[1].Key)
	mustHandle(t, bot, tgCallback(5, "free:select:"+left[0].Key))
	mustHandle(t, bot, tgCallback(6, "free:bulkreject"))
	mustHandle(t, bot, tgCallback(7, "ok:"+bot.confirmation.Key))
	remaining, e := s.freePending(now)
	if e != nil || len(remaining) != 1 || !strings.HasPrefix(remaining[0].Key, "23:") {
		t.Fatalf("selective rejection changed the queue incorrectly: %+v, %v", remaining, e)
	}
	assertFreeReviewQueue(t, fake, "Declined.", remaining[0].Key)
}

func TestTelegramFreeSlowPublicWorkDoesNotBlockAdministrator(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Chat int64 `json:"chat_id"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if body.Chat == 888 && strings.HasSuffix(r.URL.Path, "/sendMessage") {
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":10}}`)
			return
		}
		fake.serve(w, r)
	}))
	defer server.Close()
	defer close(release)
	bot.baseURL, bot.client = server.URL, server.Client()
	mustHandle(t, bot, tgCallback(1, "free:enable"))
	wait := bot.runFreeWorkers(ctx)
	defer func() { cancel(); wait() }()
	mustHandle(t, bot, freePublic(tgMessage(2, "/start"), 888))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("public worker never started")
	}
	done := make(chan error, 1)
	go func() {
		bot.opMu.Lock()
		defer bot.opMu.Unlock()
		done <- bot.handleUpdate(ctx, tgMessage(3, "/admin"))
	}()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("admin dispatch waited for public HTTP")
	}
	public := withFreeLane(ctx, freeAdmissionLane)
	bot.deferAPI(public, 10)
	if e := bot.waitAPI(ctx, false); e != nil {
		t.Fatal("public backoff reached management")
	}
}

func TestTelegramFreeBroadcastRespectsOptOutAndRestoreHold(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	s := bot.h.store
	mustHandle(t, bot, tgCallback(1, "free:enable"))
	for _, id := range []int64{888, 999} {
		if e := s.updateFreeMember(id, bot.now(), func(m *freeMember) { m.Started, m.Language = true, "en" }); e != nil {
			t.Fatal(e)
		}
	}
	if e := bot.startFreeBroadcast(context.Background(), "Test announcement", []string{"888", "999"}, "all"); e != nil {
		t.Fatal(e)
	}
	if e := s.updateFreeMember(999, bot.now(), func(m *freeMember) { m.OptOut = true }); e != nil {
		t.Fatal(e)
	}
	bot.sendFreeBroadcast(context.Background())
	bot.sendFreeBroadcast(context.Background())
	b, e := s.freeBroadcast()
	if e != nil || b.Sent != 1 || b.Skipped != 1 || fake.chatIDs[len(fake.chatIDs)-1] != 888 {
		t.Fatalf("incorrect broadcast outcome: %+v, %v", b, e)
	}
	if e := bot.startFreeBroadcast(context.Background(), "Stopped announcement", []string{"888"}, "all"); e != nil {
		t.Fatal(e)
	}
	mustHandle(t, bot, tgCallback(2, "free:disable"))
	mustHandle(t, bot, tgCallback(3, "free:enable"))
	bot.sendFreeBroadcast(context.Background())
	b, e = s.freeBroadcast()
	if e != nil || !b.Stopped || b.Next != 0 {
		t.Fatalf("emergency stop did not persist: %+v, %v", b, e)
	}
	if e = bot.startFreeBroadcast(context.Background(), "Announcement from the previous bot", []string{"888"}, "all"); e != nil {
		t.Fatal(e)
	}
	bot.mu.Lock()
	bot.cfg.Token = "678901:replacement-token-for-testing"
	bot.mu.Unlock()
	bot.sendFreeBroadcast(context.Background())
	b, e = s.freeBroadcast()
	if e != nil || !b.Stopped || b.Next != 0 {
		t.Fatalf("a campaign crossed bot credentials: %+v, %v", b, e)
	}
	if e = bot.startFreeBroadcast(context.Background(), "Pending at backup time", []string{"888"}, "all"); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	candidate, current := filepath.Join(dir, "backup.db"), filepath.Join(dir, "current.db")
	for _, path := range []string{candidate, current} {
		f, e := os.Create(path)
		if e != nil {
			t.Fatal(e)
		}
		e = s.Backup(f)
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			t.Fatalf("backup failed: %v, %v", e, closeErr)
		}
	}
	if _, e = InspectBackupFile(candidate); e != nil {
		t.Fatal(e)
	}
	if e = PreserveRestoreAdmin(candidate, current); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(candidate)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	if e = restored.submitFree(888, token()[:12], "Request after restoration", bot.now()); !errors.Is(e, errFreePaused) {
		t.Fatalf("restore allowed new issuance: %v", e)
	}
	if b, e = restored.freeBroadcast(); e != nil || b.ID != "" {
		t.Fatalf("restore carried over the campaign: %+v, %v", b, e)
	}
	bot.h.store = restored
	defer func() { bot.h.store = s }()
	if e = bot.setFreeMode("resume"); e == nil {
		t.Fatal("ordinary resume cleared the backup hold")
	}
	mustHandle(t, bot, tgCallback(4, "free:restoreresume:"+bot.freeEpoch))
	if bot.confirmation == nil || bot.confirmation.Action != "free-restore-resume" {
		t.Fatal("restore resume did not request explicit confirmation")
	}
	mustHandle(t, bot, tgCallback(5, "ok:"+bot.confirmation.Key))
	c, e := restored.freeCatalog()
	if e != nil || c.RestoreHold {
		t.Fatalf("explicit restore resume did not clear the hold: %v", e)
	}
}

func TestTelegramFreeCustomRenewalPreservesAccountSecretsAndUsage(t *testing.T) {
	bot, _, _ := newTelegramTest(t)
	s, now := bot.h.store, time.Now()
	const owner = int64(888)
	p := defaultFreePlan()
	if e := s.saveFreeCustom(owner, p, "", 0, now); e == nil {
		t.Fatal("custom operation accepted a user who had not opened the bot")
	}
	if e := s.updateFreeMember(owner, now, func(m *freeMember) { m.Started, m.Language = true, "fa" }); e != nil {
		t.Fatal(e)
	}
	if e := s.saveFreeCustom(owner, p, "", 0, now); e != nil {
		t.Fatal(e)
	}
	r, e := s.freeRequest(owner, now)
	if e != nil {
		t.Fatal(e)
	}
	old, e := s.Get(r.AccountID)
	if e != nil {
		t.Fatal(e)
	}
	key, e := s.Seed(old.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.reserve(old.ID, 87, true); e != nil {
		t.Fatal(e)
	}
	p.Input.QuotaBytes = 2 << 30
	if e = s.saveFreeCustom(owner, p, old.ID, old.Revision, now.Add(time.Minute)); e != nil {
		t.Fatal(e)
	}
	current, e := s.Get(old.ID)
	if e != nil || current.QuotaBytes != p.Input.QuotaBytes || current.Used() != 87 ||
		current.PublicKey != old.PublicKey || current.Zone != old.Zone {
		t.Fatalf("renewal lost account data: %+v, %v", current, e)
	}
	currentKey, e := s.Seed(old.ID)
	if e != nil || !bytes.Equal(currentKey, key) {
		t.Fatal("renewal changed the private account key")
	}
	if e = s.saveFreeCustom(owner, p, old.ID, old.Revision, now); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale edit result: %v", e)
	}
}

func TestTelegramFreeLinkedAccountIsSearchableByTelegramID(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	s, now := bot.h.store, bot.now()
	for _, id := range []int64{888, 999} {
		if e := s.updateFreeMember(id, now, func(m *freeMember) { m.Started = true }); e != nil {
			t.Fatal(e)
		}
	}
	in := defaultFreePlan().account("", now)
	in.Note = "Existing account with an unrelated note"
	a, e := s.Create(in)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.linkFreeAccount(888, a.ID, a.Revision, now); e != nil {
		t.Fatal(e)
	}
	if e = s.linkFreeAccount(999, a.ID, a.Revision, now); !errors.Is(e, ErrConflict) {
		t.Fatalf("existing account accepted a second owner: %v", e)
	}
	if e = bot.userPage(context.Background(), 0, "۸۸۸", false); e != nil {
		t.Fatal(e)
	}
	if k := fake.keyboards[len(fake.keyboards)-1]; len(k.Rows) == 0 || k.Rows[0][0].Data != "user:"+a.ID {
		t.Fatal("linked account was missing from search results")
	}
}
