package panel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"
)

func (t *Telegram) manageFree(ctx context.Context, data string) error {
	action, value, _ := strings.Cut(data, ":")
	switch action {
	case "manage":
		t.conversation, t.confirmation, t.freeSelected = nil, nil, nil
	case "enable", "disable", "pause", "resume":
		if e := t.setFreeMode(action); e != nil {
			return e
		}
		return t.freeOptions(ctx)
	case "restoreresume":
		catalog, e := t.h.store.freeCatalog()
		if e != nil {
			return e
		}
		if value != t.freeEpoch || !catalog.RestoreHold {
			return ErrConflict
		}
		return t.confirm(ctx, telegramConfirmation{Action: "free-restore-resume", FreeRevision: catalog.Revision},
			t.tr("Resume after reviewing account ownership, blocks and issuance in the restored backup?",
				"پس از بررسی مالکیت حساب‌ها، مسدودی‌ها و صدور در بکاپ بازیابی‌شده، سرویس عمومی باز شود؟"))
	case "stats":
		return t.freeStatsMenu(ctx)
	case "accounts":
		k := telegramKeyboard{Rows: [][]telegramButton{
			{t.button("All accounts", "همهٔ حساب‌ها", "users:0"), t.button("Active accounts", "حساب‌های فعال", "activeusers:0")},
			{t.button("Search", "جست‌وجوی حساب", "search")},
			{t.button("Custom config", "ساخت یا تمدید سفارشی", "free:custom")},
			{t.button("Telegram access", "مسدودی کاربران تلگرام", "free:access")},
			{t.button("Back to dashboard", "بازگشت به داشبورد", "free:manage")},
		}}
		_, e := t.notify(ctx, t.config(), t.tr("Accounts\nFind an existing account or issue a custom configuration.", "حساب‌ها\nیک حساب موجود را پیدا کنید یا کانفیگ سفارشی بسازید."), k)
		return e
	case "options":
		return t.freeOptions(ctx)
	case "queue", "select", "selectall", "unselect", "requestdetail", "approve", "reject", "bulkapprove", "bulkreject":
		return t.freeReviewMenu(ctx, action, value)
	case "plans", "planedit", "plannew", "planview", "planpublish", "planhide", "plandelete", "engines", "engine":
		return t.freePlanMenu(ctx, action, value)
	case "editor":
		return t.freeEditorAction(ctx, value)
	case "texts":
		k := telegramKeyboard{}
		for _, item := range []struct{ id, fa string }{{"welcome", "خوش‌آمد"}, {"terms", "شرایط"}, {"guide", "راهنما"}} {
			k.Rows = append(k.Rows, []telegramButton{
				t.button(item.id+" · English", item.fa+" · انگلیسی", "free:text:"+item.id+":en"),
				t.button(item.id+" · Persian", item.fa+" · فارسی", "free:text:"+item.id+":fa"),
			})
		}
		k.Rows = append(k.Rows, []telegramButton{t.button("Back to public settings", "بازگشت به تنظیمات عمومی", "free:options")})
		_, e := t.notify(ctx, t.config(), t.tr("Public messages\nChoose a text and language.", "متن‌های عمومی\nمتن و زبان موردنظر را انتخاب کنید."), k)
		return e
	case "text":
		if !validFreeTextField(value) {
			return ErrNotFound
		}
		c, e := t.h.store.freeCatalog()
		if e != nil {
			return e
		}
		return t.freePrompt(ctx, "text:"+value, c.Revision,
			t.tr("Reply with the new text (max 2,400 bytes). Send - for the default.\n\n", "متن جدید را در پاسخ بفرستید (حداکثر ۲۴۰۰ بایت). برای متن پیش‌فرض - بفرستید.\n\n")+c.Text[value])
	case "block", "unblock":
		id, e := strconv.ParseInt(value, 10, 64)
		if e != nil || id < 1 || id == t.config().ChatID {
			return errors.New("invalid Telegram ID")
		}
		return t.confirm(ctx, telegramConfirmation{Action: "free-" + action, FreeChat: id},
			t.tr("Change public bot access for Telegram ID ", "تغییر دسترسی عمومی ربات برای شناسهٔ تلگرام ")+value)
	case "access":
		return t.freePrompt(ctx, "access", 0, t.tr("Reply with the numeric Telegram ID to manage its public access.", "شناسهٔ عددی تلگرام را برای مدیریت دسترسی عمومی بفرستید."))
	case "custom":
		p := defaultFreePlan()
		p.ID, p.Input.Name = "custom", t.tr("Custom account", "حساب سفارشی")
		return t.startFreeEditor(ctx, "custom", 0, p)
	case "linkuser":
		account, e := t.h.store.Get(value)
		if e != nil {
			return e
		}
		return t.freePrompt(ctx, "linkuser:"+account.ID, account.Revision,
			t.tr("Reply with the numeric Telegram ID of the recipient who opened the bot.", "شناسهٔ عددی گیرنده‌ای که ربات را باز کرده بفرستید.")+"\n"+account.Name+" · "+account.Zone)
	case "broadcast", "announce", "broadcaststop":
		return t.freeBroadcastMenu(ctx, action, value)
	default:
		return errors.New("invalid free configuration action")
	}
	return t.freeDashboard(ctx)
}

func (t *Telegram) setFreeMode(action string) error {
	c := t.config()
	switch action {
	case "enable", "disable":
		c.FreeAccess = action == "enable"
		if e := WritePrivateJSON(t.file, telegramDisk{Config: c, State: t.state}); e != nil {
			return errors.New("cannot save public bot access")
		}
		t.mu.Lock()
		t.cfg = c
		t.mu.Unlock()
		t.clearFreeChallenge(0)
		if !c.FreeAccess {
			t.freeEpoch = token()[:8]
			return t.h.store.db.Update(func(tx *bolt.Tx) error {
				catalog, e := readFreeCatalog(tx)
				if e != nil {
					return e
				}
				catalog.Paused = true
				if e = saveFreeCatalog(tx, catalog); e != nil {
					return e
				}
				b, e := readFreeBroadcast(tx)
				if e != nil || b.ID == "" {
					return e
				}
				b.Stopped = true
				return putJSON(tx.Bucket(meta), freeBroadcastKey, b)
			})
		}
	case "pause", "resume":
		catalog, e := t.h.store.freeCatalog()
		if e != nil {
			return e
		}
		if action == "resume" && catalog.RestoreHold {
			return errors.New("review the restored database before reopening public access")
		}
		if e = t.h.store.changeFreeCatalog(catalog.Revision, func(c *freeCatalog) error {
			c.Paused = action == "pause"
			return nil
		}); e != nil {
			return e
		}
		t.clearFreeChallenge(0)
	}
	return nil
}

func (t *Telegram) freeDashboard(ctx context.Context) error {
	c := t.config()
	catalog, e := t.h.store.freeCatalog()
	if e != nil {
		return e
	}
	n, _, e := t.h.store.freeQueue(t.now())
	if e != nil {
		return e
	}
	k := telegramKeyboard{Rows: [][]telegramButton{
		{t.button(fmt.Sprintf("Requests (%d)", n), telegramPersianDigits.Replace(fmt.Sprintf("درخواست‌ها (%d)", n)), "free:queue")},
		{t.button("Plans and engines", "پلن‌ها و موتورها", "free:plans"), t.button("Accounts", "حساب‌ها", "free:accounts")},
		{t.button("Statistics", "آمار", "free:stats"), t.button("Broadcast", "پیام همگانی", "free:broadcast")},
		{t.button("Public access and messages", "دسترسی و متن‌های عمومی", "free:options")},
		{t.button("Server management", "مدیریت سرور", "home")},
	}}
	_, e = t.notify(ctx, c, t.tr("Free configurations", "داشبورد کانفیگ رایگان")+"\n"+t.freeAccessSummary(c, catalog), k)
	return e
}

func (t *Telegram) freeAccessSummary(c TelegramConfig, catalog freeCatalog) string {
	switch {
	case !c.FreeAccess:
		return t.tr("Public access is off.", "دسترسی عمومی خاموش است.")
	case catalog.RestoreHold:
		return t.tr("Public service is paused after a backup restore.", "سرویس عمومی پس از بازیابی پشتیبان متوقف است.")
	case catalog.Paused:
		return t.tr("New requests are paused. Existing owners can retrieve their configs.", "درخواست تازه متوقف است. صاحبان حساب همچنان می‌توانند کانفیگ بگیرند.")
	default:
		return t.tr("Accepting new requests.", "پذیرش درخواست تازه باز است.")
	}
}

func (t *Telegram) freeOptions(ctx context.Context) error {
	c := t.config()
	catalog, e := t.h.store.freeCatalog()
	if e != nil {
		return e
	}
	toggle := t.button("Enable public access", "فعال‌کردن دسترسی عمومی", "free:enable")
	k := telegramKeyboard{}
	if c.FreeAccess {
		toggle = t.button("Emergency stop", "توقف اضطراری", "free:disable")
		mode := t.button("Pause applications", "توقف پذیرش", "free:pause")
		if catalog.Paused || catalog.RestoreHold {
			mode = t.button("Resume applications", "بازکردن پذیرش", "free:resume")
			if catalog.RestoreHold {
				mode = t.button("Review complete · Resume", "بازکردن سرویس پس از بررسی", "free:restoreresume:"+t.freeEpoch)
			}
		}
		k.Rows = append(k.Rows, []telegramButton{mode})
	}
	k.Rows = append(k.Rows, []telegramButton{toggle}, []telegramButton{t.button("Edit public messages", "ویرایش متن‌های عمومی", "free:texts")},
		[]telegramButton{t.button("Back to dashboard", "بازگشت به داشبورد", "free:manage")})
	text := t.tr("Public access and messages", "دسترسی و متن‌های عمومی") + "\n" + t.freeAccessSummary(c, catalog)
	_, e = t.notify(ctx, c, text, k)
	return e
}

func (t *Telegram) executeFree(ctx context.Context, c *telegramConfirmation) error {
	switch c.Action {
	case "free-bulkapprove", "free-bulkreject":
		return t.executeFreeReview(ctx, c.Action == "free-bulkapprove", c.FreeIDs)
	case "free-restore-resume":
		if e := t.h.store.changeFreeCatalog(c.FreeRevision, func(catalog *freeCatalog) error {
			if !catalog.RestoreHold {
				return ErrConflict
			}
			catalog.RestoreHold, catalog.Paused = false, false
			return nil
		}); e != nil {
			return e
		}
	case "free-block", "free-unblock":
		if e := t.h.store.updateFreeMember(c.FreeChat, t.now(), func(m *freeMember) { m.Blocked = c.Action == "free-block" }); e != nil {
			return e
		}
		t.clearFreeChallenge(c.FreeChat)
	case "free-delete-plan":
		if e := t.h.store.changeFreeCatalog(c.FreeRevision, func(catalog *freeCatalog) error {
			catalog.Plans = slices.DeleteFunc(catalog.Plans, func(p freePlan) bool { return p.ID == c.ID })
			return nil
		}); e != nil {
			return e
		}
		return t.freePlanMenu(ctx, "plans", "")
	case "free-custom":
		if !t.config().FreeAccess || c.FreePlan == nil {
			return errFreePaused
		}
		if e := t.h.store.saveFreeCustom(c.FreeChat, *c.FreePlan, c.ID, c.Revision, t.now()); e != nil {
			return e
		}
		t.queueFreeNotice(t.config(), c.FreeChat)
		if e := t.h.manager.Reconcile(); e != nil {
			return errors.New("configuration saved; runtime preparation will be retried")
		}
	case "free-link-user":
		if !t.config().FreeAccess {
			return errFreePaused
		}
		if e := t.h.store.linkFreeAccount(c.FreeChat, c.ID, c.FreeRevision, t.now()); e != nil {
			return e
		}
		t.queueFreeNotice(t.config(), c.FreeChat)
	case "free-broadcast":
		return t.startFreeBroadcast(ctx, c.FreeText, c.FreeIDs, c.ID)
	case "free-delete-users":
		return t.deleteFreeSelection(ctx, c.FreeIDs, c.FreeRevisions)
	default:
		return errors.New("invalid free configuration confirmation")
	}
	return t.freeDashboard(ctx)
}
