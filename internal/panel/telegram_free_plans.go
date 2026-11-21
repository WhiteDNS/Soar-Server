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

func encodeFreeEngines(engines []string) (string, error) {
	if len(engines) == 0 {
		return "", nil
	}
	for name, list := range engineSelections {
		if name == "" || len(list) != len(engines) {
			continue
		}
		match := true
		for _, engine := range list {
			match = match && slices.Contains(engines, engine)
		}
		if match {
			return name, nil
		}
	}
	return "", errors.New("invalid engines: choose soar, cotten, masterdns or an allowed combination")
}

func (t *Telegram) freePlanMenu(ctx context.Context, action, value string) error {
	catalog, e := t.h.store.freeCatalog()
	if e != nil {
		return e
	}
	store := t.h.store
	k := telegramKeyboard{}
	switch action {
	case "plans":
		for _, p := range catalog.Plans {
			mark := "○"
			if catalog.offers(p) {
				mark = "●"
			}
			k.Rows = append(k.Rows, []telegramButton{{Text: mark + " " + p.Input.Name, Data: "free:planview:" + p.ID}})
		}
		k.Rows = append(k.Rows, []telegramButton{t.button("Add plan", "افزودن پلن", "free:plannew"), t.button("Public engines", "موتورهای عمومی", "free:engines")},
			[]telegramButton{t.button("Back", "بازگشت", "free:manage")})
		_, e = t.notify(ctx, t.config(), t.tr("Free plans · ● published and available", "پلن‌های رایگان · ● منتشرشده و در دسترس"), k)
		return e
	case "engines", "engine":
		engines := slices.Clone(engineSelections[catalog.Allowed])
		if catalog.Allowed == "" {
			engines = nil
		}
		if action == "engine" {
			params := strings.Split(value, ":")
			if len(params) != 3 || !slices.Contains(profileEngines, params[0]) || (params[1] != "on" && params[1] != "off") {
				return ErrNotFound
			}
			revision, err := strconv.ParseUint(params[2], 10, 64)
			if err != nil {
				return ErrConflict
			}
			pos := slices.Index(engines, params[0])
			if params[1] == "off" && pos >= 0 {
				engines = slices.Delete(engines, pos, pos+1)
			} else if params[1] == "on" && pos == -1 {
				engines = append(engines, params[0])
			}
			next, err := encodeFreeEngines(engines)
			if err != nil {
				return err
			}
			if err = store.changeFreeCatalog(revision, func(c *freeCatalog) error { c.Allowed = next; return nil }); err != nil {
				return err
			}
			return t.freePlanMenu(ctx, "engines", "")
		}
		for _, engine := range profileEngines {
			mark, toggle := "○", "on"
			if slices.Contains(engines, engine) {
				mark, toggle = "●", "off"
			}
			k.Rows = append(k.Rows, []telegramButton{{Text: mark + " " + freeEngineName(engine),
				Data: fmt.Sprintf("free:engine:%s:%s:%d", engine, toggle, catalog.Revision)}})
		}
		k.Rows = append(k.Rows, []telegramButton{t.button("Back", "بازگشت", "free:plans")})
		_, e = t.notify(ctx, t.config(), t.tr("Choose the engines available to the public.", "موتورهای مجاز برای عرضهٔ عمومی را انتخاب کنید."), k)
		return e
	}
	id, rev, _ := strings.Cut(value, ":")
	p, ok := catalog.plan(id)
	if action == "plannew" {
		if len(catalog.Plans) >= freePlanLimit {
			return errors.New("free plan capacity reached")
		}
		p = defaultFreePlan()
		p.ID, p.Published, p.Input.Name = token()[:8], false, t.tr("New plan", "پلن جدید")
	} else if !ok {
		return ErrNotFound
	}
	switch action {
	case "plannew", "planedit":
		return t.startFreeEditor(ctx, action, catalog.Revision, p)
	case "planview":
		data := fmt.Sprintf("%s:%d", p.ID, p.Revision)
		button := t.button("Publish", "انتشار", "free:planpublish:"+data)
		if p.Published {
			button = t.button("Unpublish", "توقف انتشار", "free:planhide:"+data)
		}
		k.Rows = [][]telegramButton{
			{t.button("Edit", "ویرایش", "free:planedit:"+p.ID), button},
			{t.button("Delete", "حذف", "free:plandelete:"+data), t.button("Back", "بازگشت", "free:plans")},
		}
		_, e = t.notify(ctx, t.config(), freePlanSummary(p, t.tr)+"\n"+t.freeApprovalMode(p), k)
		return e
	case "planpublish", "planhide", "plandelete":
		revision, e := strconv.ParseUint(rev, 10, 64)
		if e != nil || revision != p.Revision {
			return ErrConflict
		}
		if action == "plandelete" {
			return t.confirm(ctx, telegramConfirmation{Action: "free-delete-plan", ID: p.ID, FreeRevision: catalog.Revision},
				t.tr("Delete this free plan and stop issuing pending requests for it?", "این پلن حذف و صدور درخواست‌های باز آن متوقف شود؟")+"\n"+freePlanSummary(p, t.tr))
		}
		p.Published = action == "planpublish"
		if p.Published && !catalog.offers(p) {
			return errFreePlanUnavailable
		}
		p.Revision++
		if e = store.changeFreeCatalog(catalog.Revision, func(c *freeCatalog) error {
			c.Plans[slices.IndexFunc(c.Plans, func(plan freePlan) bool { return plan.ID == p.ID })] = p
			return nil
		}); e != nil {
			return e
		}
		return t.freePlanMenu(ctx, "planview", p.ID)
	}
	return ErrNotFound
}

func (t *Telegram) freePrompt(ctx context.Context, setting string, revision uint64, text string) error {
	t.confirmation = nil
	d := &telegramConversation{Field: "free", Setting: setting, FreeRevision: revision, Expires: t.now().Add(10 * time.Minute)}
	if menu, ok := ctx.Value(telegramMenuKey{}).(*telegramMenuMessage); ok {
		d.MenuID = menu.message
	}
	t.conversation = d
	id, e := t.notify(ctx, t.config(), text, map[string]bool{"force_reply": true, "selective": true})
	if e == nil {
		d.Prompt = id
	}
	return e
}

func (t *Telegram) freeAdminReply(ctx context.Context, d *telegramConversation, text string) error {
	kind, value, _ := strings.Cut(d.Setting, ":")
	s := t.h.store
	switch kind {
	case "limit":
		return t.saveFreeLimit(ctx, d, value, text)
	case "administrator":
		return t.administratorMenu(ctx, "roles:"+telegramDigits(text))
	case "text":
		if text == "-" {
			text = ""
		}
		if e := s.changeFreeCatalog(d.FreeRevision, func(c *freeCatalog) error {
			if c.Text == nil {
				c.Text = make(map[string]string)
			}
			c.Text[value] = text
			return nil
		}); e != nil {
			return e
		}
		t.conversation = nil
		return t.manageFree(ctx, "texts")
	case "access", "linkuser":
		text = telegramDigits(text)
		id, e := strconv.ParseInt(text, 10, 64)
		if e != nil || !validFreeChatID(text) || t.config().administratorRole(id) != "" {
			return errTelegramUserID
		}
		if kind == "linkuser" {
			a, e := s.Get(value)
			if e != nil {
				return e
			}
			if a.Revision != d.FreeRevision {
				return ErrConflict
			}
			return t.confirm(ctx, telegramConfirmation{Action: "free-link-user", ID: value, FreeChat: id, FreeRevision: a.Revision},
				fmt.Sprintf(t.tr("Link %s to Telegram ID %d? This gives the recipient access to the connection links.", "حساب %s به شناسهٔ تلگرام %d متصل شود؟ گیرنده به لینک‌های اتصال دسترسی پیدا می‌کند."), a.Name+" · "+a.Zone, id))
		}
		t.conversation = nil
		return t.freeMemberMenu(ctx, id)
	case "broadcast":
		return t.previewFreeBroadcast(ctx, value, text)
	}
	return ErrNotFound
}
