package panel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTelegramMenusRegisterPrivateCommandsAndKeepAdminScoped(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	c := bot.config()
	c.FreeAccess = true
	if err := bot.syncTelegramCommands(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	var public, persian, admin, button bool
	for _, call := range fake.menuCalls {
		var scope struct {
			Type string `json:"type"`
			Chat int64  `json:"chat_id"`
		}
		_ = json.Unmarshal(call["scope"], &scope)
		var commands []struct{ Command, Description string }
		_ = json.Unmarshal(call["commands"], &commands)
		switch scope.Type {
		case "all_private_chats":
			for _, cmd := range commands {
				if cmd.Command == "admin" || cmd.Command == "settings" {
					t.Fatal("admin command exposed to a public chat")
				}
				if cmd.Command == "free_status" {
					public = true
					persian = persian || strings.Contains(cmd.Description, "حساب")
				}
			}
		case "chat":
			for _, cmd := range commands {
				admin = admin || (scope.Chat == c.ChatID && cmd.Command == "admin")
			}
		default:
			button = button || strings.Contains(string(call["menu_button"]), `"commands"`)
		}
	}
	if !public || !persian || !admin || !button {
		t.Fatalf("incomplete menu registration: public=%t fa=%t admin=%t button=%t", public, persian, admin, button)
	}
	fake.menuCalls = nil
	c.FreeAccess = false
	if err := bot.syncTelegramCommands(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	for _, call := range fake.menuCalls {
		if strings.Contains(string(call["scope"]), "all_private_chats") && string(call["commands"]) != "[]" {
			t.Fatal("public commands remain after disabling access")
		}
	}
}

func TestTelegramMenusNavigateInPlaceForBothRoles(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	now := bot.now()
	bot.now = func() time.Time { return now }
	admin := tgCallback(1, "free:enable")
	admin.Callback.Message.ID = 80
	mustHandle(t, bot, admin)

	start := freePublic(tgMessage(2, "/start"), 991)
	start.Message.From.Language = "fa"
	mustHandle(t, bot, start)
	messageID := fake.nextID
	for i, item := range []struct{ action, text string }{
		{"free:request", "یک پلن رایگان انتخاب کنید"},
		{"free:home", "کانفیگ رایگان"},
		{"free:status", "هنوز درخواست باز یا حسابی ندارید"},
		{"free:guide", "حساب من"},
		{"free:language", "زبان"},
	} {
		now = now.Add(time.Second)
		u := freePublic(tgCallback(int64(i+3), item.action), 991)
		u.Callback.Message.ID = messageID
		mustHandle(t, bot, u)
		if got := fake.texts[len(fake.texts)-1]; !strings.Contains(got, item.text) {
			t.Fatalf("%s response = %q", item.action, got)
		}
	}
	if fake.calls["sendMessage"] != 1 || fake.calls["editMessageText"] != 6 {
		t.Fatalf("ordinary navigation flooded the chat: %+v", fake.calls)
	}
}

func TestTelegramMenusEditWithColorAndFallBackOnlyWhenNeeded(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	c := bot.config()
	k := telegramKeyboard{Rows: [][]telegramButton{{{Text: "ثبت", Data: "free:request"}, {Text: "تأیید", Data: "ok:token"}, {Text: "رد", Data: "free:reject:key"}}}}
	send := func() error {
		_, err := bot.sendText(withTelegramMenu(context.Background(), c.ChatID, 43), c, "منو", k)
		return err
	}
	if err := send(); err != nil {
		t.Fatal(err)
	}
	if fake.calls["editMessageText"] != 1 || fake.calls["sendMessage"] != 0 {
		t.Fatal("navigation created a new message")
	}
	buttons := fake.keyboards[0].Rows[0]
	if buttons[0].Style != "primary" || buttons[1].Style != "success" || buttons[2].Style != "danger" {
		t.Fatalf("incorrect button styles: %+v", buttons)
	}
	fake.editMode = "Bad Request: message is not modified"
	if err := send(); err != nil || fake.calls["sendMessage"] != 0 {
		t.Fatalf("identical edit created a message: %v", err)
	}
	fake.editMode = "Bad Request: message to edit not found"
	if err := send(); err != nil || fake.calls["sendMessage"] != 1 {
		t.Fatalf("missing message did not recover: %v", err)
	}
}

func TestTelegramMenusCustomEditorIssuesOnlyAfterConfirmation(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	c := bot.config()
	c.Language = "fa"
	bot.mu.Lock()
	bot.cfg = c
	bot.mu.Unlock()
	mustHandle(t, bot, tgCallback(1, "free:enable"))
	mustHandle(t, bot, freePublic(tgMessage(2, "/start"), 991))
	mustHandle(t, bot, tgCallback(3, "free:custom"))
	d := bot.conversation
	if d == nil || d.FreePlan == nil {
		t.Fatal("custom editor was not opened")
	}
	prefix := "free:editor:" + d.Key + ":"
	mustHandle(t, bot, tgCallback(4, prefix+"save"))
	if bot.confirmation != nil || !strings.Contains(fake.texts[len(fake.texts)-1], "گیرنده را انتخاب کنید") {
		t.Fatal("editor tried to save without a recipient")
	}
	mustHandle(t, bot, tgCallback(5, prefix+"chat"))
	oldPrompt := d.Prompt
	mustHandle(t, bot, freeReply(tgMessage(6, "۱۲۳۴۵"), oldPrompt))
	if d.FreeChat != 0 || d.Prompt == oldPrompt {
		t.Fatal("editor accepted the administrator as a public recipient or did not retry")
	}
	mustHandle(t, bot, freeReply(tgMessage(7, "۹۹۱"), d.Prompt))
	mustHandle(t, bot, tgCallback(8, prefix+"save"))
	if bot.confirmation == nil || bot.confirmation.FreeChat != 991 {
		t.Fatal("custom configuration was not ready for confirmation")
	}
	if bot.h.store.hasFreeOwner(991) {
		t.Fatal("account was issued before confirmation")
	}
	mustHandle(t, bot, tgCallback(9, "ok:"+bot.confirmation.Key))
	if !bot.h.store.hasFreeOwner(991) {
		t.Fatal("confirmed custom configuration did not create an account")
	}
}

func TestTelegramMenusNotifyNewRequestsOnceAndBatchBursts(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	now := bot.now()
	c := bot.config()
	c.FreeAccess, c.Language = true, "fa"
	bot.mu.Lock()
	bot.cfg = c
	bot.mu.Unlock()
	for _, id := range []int64{101, 102} {
		if err := bot.h.store.submitFree(id, token()[:12], "A request for test access", now); err != nil {
			t.Fatal(err)
		}
	}
	fake.fail = "sendMessage"
	if err := bot.sendFreeRequestDigest(context.Background(), c, now); err == nil {
		t.Fatal("failed delivery was reported as a success")
	}
	fake.fail = ""
	for _, at := range []time.Time{now, now.Add(time.Minute)} {
		if err := bot.sendFreeRequestDigest(context.Background(), c, at); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := bot.h.store.freePending(now)
	if err != nil || len(pending) != 2 || !pending[0].Notified || !pending[1].Notified {
		t.Fatalf("delivery marks were not persisted: %v", err)
	}
	if len(fake.texts) != 1 || fake.chatIDs[0] != c.ChatID || !strings.Contains(fake.texts[0], "۲") ||
		fake.keyboards[0].Rows[0][0].Data != "free:queue" {
		t.Fatalf("notification was not batched, localized and sent once: %v", fake.texts)
	}
}
