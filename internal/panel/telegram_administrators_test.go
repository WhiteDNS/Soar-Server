package panel

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTelegramAdministratorsChooseRolesAndRevokeImmediately(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	const limited, full, outsider = int64(801), int64(802), int64(803)
	owner := bot.config().ChatID
	var updateID int64
	tap := func(id int64, data string) {
		updateID++
		mustHandle(t, bot, freePublic(tgCallback(updateID, data), id))
	}
	message := func(id int64, text string) {
		updateID++
		mustHandle(t, bot, freePublic(tgMessage(updateID, text), id))
	}
	grant := func(id int64, role string) {
		tap(owner, "admins:"+role+":"+strconv.FormatInt(id, 10))
		if bot.confirmation == nil || bot.config().administratorRole(id) == role {
			t.Fatal("role change was not waiting for the owner")
		}
		tap(owner, "ok:"+bot.confirmation.Key)
		if bot.config().administratorRole(id) != role {
			t.Fatal("owner confirmation did not save the role")
		}
	}

	tap(owner, "admins:add")
	if bot.conversation == nil || bot.conversation.Prompt == 0 {
		t.Fatal("missing numeric ID prompt")
	}
	updateID++
	mustHandle(t, bot, freeReply(tgMessage(updateID, "۸۰۱"), bot.conversation.Prompt))
	if !strings.Contains(fake.texts[len(fake.texts)-1], "801") {
		t.Fatal("role chooser did not show the target")
	}
	grant(limited, telegramFreeAdmin)
	grant(full, telegramFullAdmin)
	reloaded, e := NewTelegram(bot.h, bot.file)
	if e != nil || reloaded.config().administratorRole(limited) != telegramFreeAdmin ||
		reloaded.config().administratorRole(full) != telegramFullAdmin {
		t.Fatalf("roles did not survive restart: %v", e)
	}
	bot.h.telegram = bot

	count := len(fake.texts)
	message(outsider, "/admin")
	if len(fake.texts) != count {
		t.Fatal("unauthorized account reached administration")
	}
	message(limited, "/start")
	if fake.chatIDs[len(fake.chatIDs)-1] != limited || !strings.Contains(fake.texts[len(fake.texts)-1], "Free configurations") {
		t.Fatal("limited role did not get its own dashboard")
	}
	message(limited, "/backup")
	if !strings.Contains(fake.texts[len(fake.texts)-1], "requires a different administrator role") {
		t.Fatal("limited admin got access to backups")
	}
	tap(limited, "users:0")
	if !strings.Contains(fake.texts[len(fake.texts)-1], "requires a different administrator role") {
		t.Fatal("limited admin accessed server accounts")
	}
	message(full, "/backup")
	if fake.chatIDs[len(fake.chatIDs)-1] != full || !strings.Contains(fake.texts[len(fake.texts)-1], "Encrypted backups") {
		t.Fatal("full role could not open backups in its own chat")
	}
	tap(full, "admins:remove:801")
	if !strings.Contains(fake.texts[len(fake.texts)-1], "requires a different administrator role") ||
		bot.config().administratorRole(limited) != telegramFreeAdmin {
		t.Fatal("full role accessed owner-only role management")
	}
	tap(limited, "free:plannew")
	first := bot.adminDialogues[limited].conversation
	tap(full, "free:plannew")
	second := bot.adminDialogues[full].conversation
	if first == nil || second == nil || first.Key == second.Key || bot.conversation != nil {
		t.Fatal("administrators shared the same editor state")
	}
	tap(full, "free:editor:"+first.Key+":save")
	catalog, _ := bot.h.store.freeCatalog()
	if len(catalog.Plans) != 1 {
		t.Fatal("an administrator used another administrator's editor")
	}
	tap(limited, "free:editor:"+first.Key+":save")
	catalog, _ = bot.h.store.freeCatalog()
	if len(catalog.Plans) != 2 {
		t.Fatal("the matching administrator could not save the draft")
	}
	grant(full, telegramFreeAdmin)
	if bot.adminDialogues[full].conversation != nil {
		t.Fatal("changing the role kept the old session")
	}
	tap(full, "free:plannew")
	revoked := bot.adminDialogues[full].conversation
	tap(owner, "admins:remove:802")
	if bot.confirmation == nil || bot.config().administratorRole(full) == "" {
		t.Fatal("the role was removed before confirmation")
	}
	ownerConfirmation := bot.confirmation.Key
	tap(full, "ok:"+ownerConfirmation)
	if bot.config().administratorRole(full) == "" || bot.confirmation.Key != ownerConfirmation {
		t.Fatal("a delegate consumed the owner's confirmation")
	}
	tap(owner, "ok:"+ownerConfirmation)
	if bot.config().administratorRole(full) != "" || bot.adminDialogues[full].conversation != nil ||
		fake.calls["deleteMyCommands"] != 1 {
		t.Fatal("removal did not revoke authorization, session and commands")
	}
	count = len(fake.texts)
	tap(full, "free:editor:"+revoked.Key+":save")
	message(full, "/admin")
	if len(fake.texts) != count {
		t.Fatal("removed administrator could still manage the bot")
	}
}

func TestTelegramAdministratorsEachReceiveTheRequestsDigestOnce(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	c := bot.config()
	c.FreeAccess = true
	c.Administrators = []telegramAdministrator{{ID: 801, Role: telegramFreeAdmin}, {ID: 802, Role: telegramFullAdmin}}
	bot.mu.Lock()
	bot.cfg = c
	bot.mu.Unlock()
	now := bot.now()
	if e := bot.h.store.submitFree(804, token()[:12], "Request a connection for my laptop.", now); e != nil {
		t.Fatal(e)
	}
	for _, at := range []time.Time{now, now.Add(time.Minute)} {
		if e := bot.sendFreeRequestDigest(context.Background(), c, at); e != nil {
			t.Fatal(e)
		}
	}
	if want := []int64{c.ChatID, 801, 802}; !slices.Equal(fake.chatIDs, want) {
		t.Fatalf("unexpected request digest recipients: %v", fake.chatIDs)
	}
}
