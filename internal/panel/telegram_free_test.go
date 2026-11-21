package panel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (s *Store) submitFree(chatID int64, nonce, reason string, now time.Time) error {
	return s.submitFreePlan(chatID, nonce, reason, defaultFreePlan(), now)
}

func TestTelegramFreeConfigRequiresPrivateReplyAndAdminApproval(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	now := time.Now().UTC()
	bot.now = func() time.Time { return now }
	public := func(id int64, text string) telegramUpdate {
		u := tgMessage(id, text)
		u.Message.From.ID, u.Message.Chat.ID = 888, 888
		return u
	}
	mustHandle(t, bot, public(1, "/start"))
	if len(fake.texts) != 0 {
		t.Fatal("public enrollment is enabled by default")
	}
	mustHandle(t, bot, tgCallback(2, "free:enable"))
	mustHandle(t, bot, public(3, "/free"))
	if len(fake.texts) == 0 || bot.freeChallenges[888].Prompt == 0 {
		t.Fatal("challenge was not delivered")
	}
	challenge := bot.freeChallenges[888]
	reply := public(4, challenge.Code+" I need a connection for my phone.")
	mustHandle(t, bot, reply)
	if count, _, _ := bot.h.store.freeQueue(bot.now()); count != 0 {
		t.Fatal("message without a reply was accepted")
	}
	reply.ID++
	reply.Message.Reply = &struct {
		ID int64 `json:"message_id"`
	}{challenge.Prompt}
	now = now.Add(4 * time.Second)
	mustHandle(t, bot, reply)
	count, queued, err := bot.h.store.freeQueue(bot.now())
	if err != nil || count != 1 {
		t.Fatalf("request missing: %d, %v", count, err)
	}
	live := bot
	bot, err = NewTelegram(live.h, live.file)
	if err != nil {
		t.Fatal(err)
	}
	bot.now, bot.baseURL, bot.client, bot.sendInterval = live.now, live.baseURL, live.client, 0
	if !bot.config().FreeAccess {
		t.Fatal("enrollment setting did not survive restart")
	}
	key := queued.Key + "@" + bot.freeEpoch
	approval := tgCallback(6, "free:approve:"+key)
	approval.Callback.From.ID, approval.Callback.Message.Chat.ID = 888, 888
	mustHandle(t, bot, approval)
	if accounts, _ := bot.h.store.List(); len(accounts) != 0 {
		t.Fatal("public approval created a config")
	}
	mustHandle(t, bot, tgCallback(7, "free:approve:"+key))
	mustHandle(t, bot, tgCallback(8, "free:approve:"+key))
	accounts, err := bot.h.store.List()
	if err != nil || len(accounts) != 1 || accounts[0].QuotaBytes != 1<<30 || accounts[0].Engines != "masterdns" {
		t.Fatalf("unexpected accounts after repeated approval: %+v, %v", accounts, err)
	}
	u := public(9, "/free_config")
	u.Message.Chat.Type = "group"
	messages := len(fake.texts)
	mustHandle(t, bot, u)
	if len(fake.texts) != messages {
		t.Fatal("configuration exposed in a group")
	}
	now = now.Add(4 * time.Second)
	mustHandle(t, bot, public(10, "/free_config"))
	if !strings.Contains(fake.texts[len(fake.texts)-1], "masterdns://") || fake.chatIDs[len(fake.chatIDs)-1] != 888 {
		t.Fatalf("owner could not recover the config: %q", fake.texts[len(fake.texts)-1])
	}
	now = now.Add(4 * time.Second)
	mustHandle(t, bot, public(11, "/free_qr"))
	if fake.photos != 1 {
		t.Fatal("owner could not get the QR")
	}
	other := public(12, "/free_config")
	other.Message.From.ID, other.Message.Chat.ID = 999, 999
	mustHandle(t, bot, other)
	if strings.Contains(fake.texts[len(fake.texts)-1], "masterdns://") {
		t.Fatal("configuration exposed to a different Telegram account")
	}

	backup := filepath.Join(t.TempDir(), "accounts.db")
	w, err := os.Create(backup)
	if err != nil {
		t.Fatal(err)
	}
	err = bot.h.store.BackupContext(context.Background(), w)
	closeErr := w.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("backup failed: %v, %v", err, closeErr)
	}
	if _, err := InspectBackupFile(backup); err != nil {
		t.Fatalf("could not validate backup with a Telegram account: %v", err)
	}
}

func TestTelegramFreeQueueAndIssuanceLimits(t *testing.T) {
	bot, _, _ := newTelegramTest(t)
	store := bot.h.store
	now := bot.now()
	for i := int64(1); i <= freeQueueLimit; i++ {
		if err := store.submitFree(i, token()[:12], "test submission", now); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.submitFree(999, token()[:12], "overflow request", now); err != errFreeQueueFull {
		t.Fatalf("queue overflow: %v", err)
	}
	var first Client
	for i := 0; i <= freeDailyLimit; i++ {
		_, req, err := store.freeQueue(now)
		if err != nil {
			t.Fatal(err)
		}
		approved, err := store.approveFree(req.Key, now)
		if i == freeDailyLimit {
			if err != errFreeDailyLimit {
				t.Fatalf("daily limit bypassed: %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = approved
		}
	}
	if err := store.Delete(first.ID); err != nil {
		t.Fatal(err)
	}
	_, req, err := store.freeQueue(now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.approveFree(req.Key, now); err != errFreeDailyLimit {
		t.Fatalf("deletion bypassed daily limit: %v", err)
	}
	if count, _, err := store.freeQueue(now.Add(25 * time.Hour)); err != nil || count != 0 {
		t.Fatalf("expired requests still pending: %d, %v", count, err)
	}
	if _, err = store.approveFree(req.Key, now.Add(25*time.Hour)); err != ErrNotFound {
		t.Fatalf("expired approval accepted: %v", err)
	}
}

func TestTelegramFreeFloodDoesNotReachProvisioningOrManagement(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	mustHandle(t, bot, tgCallback(1, "free:enable"))
	for i := int64(2); i <= 2000; i++ {
		u := tgMessage(i, "/free")
		u.Message.From.ID, u.Message.Chat.ID = i+100, i+100
		mustHandle(t, bot, u)
	}
	if len(bot.freeChallenges) != freeTrafficLimit-4 || fake.calls["sendMessage"] != 1+freeTrafficLimit-4 {
		t.Fatal("public flood bypassed global rate limit")
	}
	if accounts, _ := bot.h.store.List(); len(accounts) != 0 {
		t.Fatal("public flood provisioned accounts")
	}
	mustHandle(t, bot, tgMessage(2001, "/start"))
	if fake.calls["sendMessage"] != 2+freeTrafficLimit-4 {
		t.Fatal("public traffic consumed the management allowance")
	}
}
