package panel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

var freeEditorFields = map[string][2]string{
	"chat": {"Telegram ID", "شناسهٔ تلگرام"}, "name": {"Name", "نام"},
	"quota": {"Volume (MiB)", "حجم (مبی‌بایت)"}, "days": {"Days", "مدت (روز)"},
	"rate": {"Speed (KiB/s)", "سرعت (کیبی‌بایت بر ثانیه)"}, "streams": {"Streams", "اتصال‌های هم‌زمان"},
	"note": {"Description", "توضیحات"}, "soar": {"Soar sessions", "نشست‌های Soar"},
	"cotten": {"CottenDNS sessions", "نشست‌های CottenDNS"}, "masterdns": {"MasterDNS sessions", "نشست‌های MasterDNS"},
}

func (t *Telegram) startFreeEditor(ctx context.Context, kind string, revision uint64, p freePlan) error {
	return t.startFreeEditorFor(ctx, kind, revision, p, 0)
}

func (t *Telegram) startFreeEditorFor(ctx context.Context, kind string, revision uint64, p freePlan, id int64) error {
	t.confirmation = nil
	d := &telegramConversation{Key: token()[:8], Setting: kind, FreeRevision: revision, FreePlan: &p, Expires: t.now().Add(10 * time.Minute)}
	if id != 0 {
		if e := t.setFreeEditorRecipient(d, id); e != nil {
			return e
		}
	}
	t.conversation = d
	return t.showFreeEditor(ctx, d, "", "")
}

func (t *Telegram) setFreeEditorRecipient(d *telegramConversation, id int64) error {
	s := t.h.store
	owned, e := s.freeRequest(id, t.now())
	if e != nil {
		return e
	}
	var account Client
	if owned.AccountID != "" {
		account, e = s.Get(owned.AccountID)
		if errors.Is(e, ErrNotFound) && !owned.RetryAllowed {
			return errFreeRetryRequired
		}
		if e != nil && !errors.Is(e, ErrNotFound) {
			return e
		}
	}
	if account.ID != "" {
		p := owned.plan()
		p.ID = "custom"
		in := accountInput(account)
		in.Name = strings.TrimPrefix(in.Name, "Telegram · ")
		in.Note = p.Input.Note
		p.Input = in
		if p.validate() != nil {
			return errFreeExistingPlan
		}
		*d.FreePlan = p
	}
	d.FreeChat, d.ID, d.FreeRevision = id, account.ID, account.Revision
	return nil
}

func (t *Telegram) showFreeEditor(ctx context.Context, d *telegramConversation, page, message string) error {
	d.Field, d.Prompt = "", 0
	prefix := "free:editor:" + d.Key + ":"
	button := func(field string) telegramButton {
		label := freeEditorFields[field]
		return t.button(label[0], label[1], prefix+field)
	}
	engines := engineSelections[d.FreePlan.Input.Engines]
	k := telegramKeyboard{}
	switch page {
	case "engines":
		for _, engine := range profileEngines {
			b := telegramButton{Text: freeEngineName(engine), Data: prefix + "engine:" + engine}
			if slices.Contains(engines, engine) {
				b.Text, b.Style = "✓ "+b.Text, "success"
			}
			k.Rows = append(k.Rows, []telegramButton{b})
		}
		k.Rows = append(k.Rows, []telegramButton{t.button("Back to editor", "بازگشت به فرم", prefix+"main")})
	case "limits":
		k.Rows = [][]telegramButton{{button("rate")}, {button("streams"), button("note")}}
		for _, engine := range engines {
			k.Rows = append(k.Rows, []telegramButton{button(engine)})
		}
		k.Rows = append(k.Rows, []telegramButton{t.button("Back to editor", "بازگشت به فرم", prefix+"main")})
	default:
		if d.Setting == "custom" {
			k.Rows = append(k.Rows, []telegramButton{button("chat")})
		}
		save := t.button("Save", "ذخیره", prefix+"save")
		save.Style = "success"
		k.Rows = append(k.Rows,
			[]telegramButton{button("name"), t.button("Engines", "موتورها", prefix+"engines")},
			[]telegramButton{button("quota"), button("days")},
			[]telegramButton{t.button("Speed and other limits", "سرعت و محدودیت‌های دیگر", prefix+"limits")})
		if d.Setting != "custom" && telegramActor(ctx).Role != telegramFreeAdmin {
			k.Rows = append(k.Rows, []telegramButton{{Text: t.freeApprovalMode(*d.FreePlan), Data: prefix + "approval"}})
		}
		k.Rows = append(k.Rows, []telegramButton{save, t.button("Cancel", "لغو", prefix+"cancel")})
	}
	title := t.tr("Edit plan", "ویرایش پلن")
	if d.Setting == "custom" {
		recipient := t.tr("Choose a recipient.", "گیرنده را انتخاب کنید.")
		if d.FreeChat != 0 {
			recipient = fmt.Sprintf("%s %d", t.tr("Telegram ID", "شناسهٔ تلگرام"), d.FreeChat)
		}
		title = t.tr("Custom configuration", "کانفیگ سفارشی") + "\n" + recipient
	}
	id, e := t.notify(ctx, t.config(), title+"\n"+freePlanSummary(*d.FreePlan, t.tr)+message, k)
	if e == nil {
		d.MenuID = id
	}
	return e
}

func (t *Telegram) freeEditorAction(ctx context.Context, value string) error {
	key, action, _ := strings.Cut(value, ":")
	d := t.conversation
	if d == nil || key != d.Key || d.FreePlan == nil {
		return ErrConflict
	}
	switch action {
	case "main", "engines", "limits":
		return t.showFreeEditor(ctx, d, action, "")
	case "approval":
		if d.Setting == "custom" || telegramActor(ctx).Role == telegramFreeAdmin {
			return ErrNotFound
		}
		d.FreePlan.AutoApprove = !d.FreePlan.AutoApprove
		return t.showFreeEditor(ctx, d, "main", "\n"+t.tr(
			"Automatic approval applies to new verified requests when capacity is available. Save to apply.",
			"پذیرش خودکار برای درخواست تازهٔ تأییدشده و در صورت وجود ظرفیت انجام می‌شود. برای اعمال تغییر، ذخیره کنید."))
	case "cancel":
		t.conversation = nil
		if d.Setting == "custom" {
			return t.manageFree(ctx, "accounts")
		}
		return t.freePlanMenu(ctx, "plans", "")
	case "save":
		return t.saveFreeEditor(ctx, d)
	}
	if engine, ok := strings.CutPrefix(action, "engine:"); ok && slices.Contains(profileEngines, engine) {
		engines := slices.Clone(engineSelections[d.FreePlan.Input.Engines])
		if i := slices.Index(engines, engine); i < 0 {
			engines = append(engines, engine)
		} else if len(engines) > 1 {
			engines = slices.Delete(engines, i, i+1)
		} else {
			return t.showFreeEditor(ctx, d, "engines", t.tr("\nSelect another engine first.", "\nابتدا یک موتور دیگر انتخاب کنید."))
		}
		choice, e := encodeFreeEngines(engines)
		if e != nil {
			return e
		}
		d.FreePlan.Input.Engines = choice
		return t.showFreeEditor(ctx, d, "engines", "")
	}
	if _, ok := freeEditorFields[action]; !ok || action == "chat" && d.Setting != "custom" {
		return ErrNotFound
	}
	return t.promptFreeEditor(ctx, d, action, "")
}

func (t *Telegram) freeApprovalMode(p freePlan) string {
	if p.AutoApprove {
		return t.tr("Approval · Automatic", "پذیرش · خودکار")
	}
	return t.tr("Approval · Manual", "پذیرش · دستی")
}

func (t *Telegram) promptFreeEditor(ctx context.Context, d *telegramConversation, field, message string) error {
	label := freeEditorFields[field]
	hint := ""
	switch field {
	case "chat":
		hint = t.tr("The recipient must have opened the bot.", "گیرنده باید ربات را قبلاً باز کرده باشد.")
	case "quota":
		hint = t.tr("For example, 1024 for 1 GiB.", "مثلاً برای یک گیبی‌بایت، ۱۰۲۴ را بفرستید.")
	case "days":
		hint = t.tr("Enter a number from 1 to 365.", "عددی بین ۱ و ۳۶۵ بفرستید.")
	case "note":
		hint = t.tr("Send - to clear the description.", "برای پاک‌کردن توضیحات، - بفرستید.")
	}
	text := message + t.tr("Reply to set this field.", "مقدار این فیلد را در پاسخ بفرستید.") + "\n" + t.tr(label[0], label[1])
	if hint != "" {
		text += "\n" + hint
	}
	id, e := t.notify(ctx, t.config(), text, map[string]bool{"force_reply": true, "selective": true})
	if e == nil {
		d.Field, d.Prompt = field, id
	}
	return e
}

func (t *Telegram) freeEditorReply(ctx context.Context, d *telegramConversation, text string) error {
	p := *d.FreePlan
	v := telegramDigits(text)
	switch d.Field {
	case "chat", "quota", "days":
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n < 1 {
			return t.promptFreeEditor(ctx, d, d.Field, t.tr("Enter a valid number.\n", "عدد معتبر بفرستید.\n"))
		}
		switch d.Field {
		case "chat":
			if !validFreeChatID(v) || t.config().administratorRole(n) != "" {
				return t.promptFreeEditor(ctx, d, "chat", t.tr("Enter a public user's Telegram ID.\n", "شناسهٔ عددی یک کاربر عمومی را بفرستید.\n"))
			}
			if e := t.setFreeEditorRecipient(d, n); e != nil {
				return t.promptFreeEditor(ctx, d, "chat", t.errorText(e)+"\n")
			}
			return t.showFreeEditor(ctx, d, "main", "")
		case "quota":
			if n > 1<<20 {
				return t.promptFreeEditor(ctx, d, "quota", t.tr("The maximum is 1,048,576 MiB.\n", "حداکثر حجم ۱٬۰۴۸٬۵۷۶ مبی‌بایت است.\n"))
			}
			p.Input.QuotaBytes = n << 20
		case "days":
			if n > 365 {
				return t.promptFreeEditor(ctx, d, "days", t.tr("The maximum is 365 days.\n", "حداکثر مدت ۳۶۵ روز است.\n"))
			}
			p.Days = int(n)
		}
	default:
		input := telegramConversation{Input: p.Input}
		if d.Field == "rate" {
			text = strings.ReplaceAll(text, "٫", ".")
		}
		if e := t.setField(&input, d.Field, text); e != nil {
			return t.promptFreeEditor(ctx, d, d.Field, t.errorText(e)+"\n")
		}
		p.Input = input.Input
	}
	if e := p.validate(); e != nil {
		return t.promptFreeEditor(ctx, d, d.Field, t.errorText(e)+"\n")
	}
	*d.FreePlan = p
	page := "limits"
	switch d.Field {
	case "chat", "name", "quota", "days":
		page = "main"
	}
	return t.showFreeEditor(ctx, d, page, "")
}

func (t *Telegram) saveFreeEditor(ctx context.Context, d *telegramConversation) error {
	s := t.h.store
	p := *d.FreePlan
	if e := p.validate(); e != nil {
		return e
	}
	if d.Setting == "custom" {
		if d.FreeChat == 0 {
			return t.showFreeEditor(ctx, d, "", t.tr("\nChoose a recipient before saving.", "\nپیش از ذخیره گیرنده را انتخاب کنید."))
		}
		if t.config().administratorRole(d.FreeChat) != "" {
			return errTelegramUserID
		}
		d.Field, d.Prompt = "", 0
		return t.confirm(ctx, telegramConfirmation{Action: "free-custom", FreeChat: d.FreeChat, FreePlan: &p, ID: d.ID, Revision: d.FreeRevision},
			fmt.Sprintf("%s %d\n%s", t.tr("Save custom configuration for Telegram ID", "ذخیرهٔ کانفیگ سفارشی برای شناسهٔ تلگرام"), d.FreeChat, freePlanSummary(p, t.tr)))
	}
	if e := s.changeFreeCatalog(d.FreeRevision, func(c *freeCatalog) error {
		if d.Setting == "plannew" {
			c.Plans = append(c.Plans, p)
			return nil
		}
		i := slices.IndexFunc(c.Plans, func(plan freePlan) bool { return plan.ID == p.ID })
		if i < 0 {
			return ErrConflict
		}
		p.Revision++
		c.Plans[i] = p
		return nil
	}); e != nil {
		return e
	}
	t.conversation = nil
	return t.freePlanMenu(ctx, "planview", p.ID)
}
