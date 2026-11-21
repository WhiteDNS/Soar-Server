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
		p.ID, p.Published, p.Input.Name = token()[:8], false, "New plan"
	} else if !ok {
		return ErrNotFound
	}
	switch action {
	case "plannew", "planedit":
		return t.freePrompt(ctx, action, catalog.Revision, &p,
			t.tr("Copy and edit the form below. Engines can be soar, cotten, masterdns or a comma-separated combination.\n\n",
				"فرم زیر را کپی و ویرایش کنید. موتور را soar، cotten، masterdns یا ترکیبی با ویرگول بنویسید.\n\n")+freePlanForm(p))
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
		_, e = t.notify(ctx, t.config(), freePlanSummary(p, t.tr), k)
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

func freePlanForm(p freePlan) string {
	i := p.Input
	return fmt.Sprintf("name: %s\nengines: %s\nvolume_mib: %d\ndays: %d\nspeed_kib: %d\nstreams: %d\nsoar_sessions: %d\ncotten_sessions: %d\nmasterdns_sessions: %d\ndescription: %s",
		i.Name, strings.Join(engineSelections[i.Engines], ","), i.QuotaBytes>>20, p.Days, i.RateBytes>>10, i.StreamLimit,
		*i.SoarSessionLimit, *i.CottenSessionLimit, *i.MasterDNSSessionLimit, i.Note)
}

func parseFreePlan(p freePlan, text string, custom bool) (freePlan, int64, error) {
	fields := make(map[string]string, 11)
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if _, exists := fields[key]; !ok || exists {
			return p, 0, errors.New("invalid plan form; use each field once")
		}
		fields[key] = strings.TrimSpace(value)
	}
	expected := 10
	if custom {
		expected++
	}
	if _, ok := fields["description"]; !ok || len(fields) != expected {
		return p, 0, errors.New("invalid plan form; include all fields shown in the prompt")
	}
	engines := strings.FieldsFunc(strings.ToLower(fields["engines"]), func(r rune) bool { return r == ',' || r == '،' || r == '+' || r == ' ' })
	if len(engines) == 1 && engines[0] == "all" {
		engines = profileEngines
	}
	choice, err := encodeFreeEngines(engines)
	if err != nil || choice == "" {
		return p, 0, errors.New("invalid engine selection")
	}
	number := func(key string, low, high int64) (int64, error) {
		value, e := strconv.ParseInt(telegramDigits(fields[key]), 10, 64)
		if e != nil || value < low || value > high {
			return 0, fmt.Errorf("invalid %s (%d–%d)", key, low, high)
		}
		return value, nil
	}
	var id int64
	if custom {
		if id, err = number("telegram_id", 1, 1<<52-1); err != nil {
			return p, 0, err
		}
	}
	nums := make(map[string]int64, 7)
	for _, spec := range []struct {
		name string
		max  int64
	}{{"volume_mib", 1 << 20}, {"days", 365}, {"speed_kib", 1 << 20}, {"streams", 128},
		{"soar_sessions", MaxSoarSessionLimit}, {"cotten_sessions", MaxCottenSessionLimit}, {"masterdns_sessions", MaxMasterDNSSessionLimit}} {
		n, e := number(spec.name, 1, spec.max)
		if e != nil {
			return p, 0, e
		}
		nums[spec.name] = n
	}
	soar, cotten, master := int(nums["soar_sessions"]), int(nums["cotten_sessions"]), int(nums["masterdns_sessions"])
	p.Input = ClientInput{Name: fields["name"], Note: fields["description"], Enabled: true, Engines: choice,
		QuotaBytes: nums["volume_mib"] << 20, RateBytes: nums["speed_kib"] << 10, StreamLimit: int(nums["streams"]),
		SoarSessionLimit: &soar, CottenSessionLimit: &cotten, MasterDNSSessionLimit: &master}
	p.Days = int(nums["days"])
	return p, id, p.validate()
}

func (t *Telegram) freePrompt(ctx context.Context, setting string, revision uint64, plan *freePlan, text string) error {
	t.confirmation = nil
	d := &telegramConversation{Field: "free", Setting: setting, FreeRevision: revision, FreePlan: plan, Expires: t.now().Add(10 * time.Minute)}
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
	case "plannew", "planedit", "custom":
		if d.FreePlan == nil {
			return ErrConflict
		}
		p, id, e := parseFreePlan(*d.FreePlan, text, kind == "custom")
		if e != nil {
			return e
		}
		if kind == "custom" {
			if id == t.config().ChatID {
				return errors.New("select a public Telegram user")
			}
			owned, e := s.freeRequest(id, t.now())
			if e != nil {
				return e
			}
			var revision uint64
			if owned.AccountID != "" {
				a, err := s.Get(owned.AccountID)
				if err != nil {
					return err
				}
				revision = a.Revision
			}
			return t.confirm(ctx, telegramConfirmation{Action: "free-custom", FreeChat: id, FreePlan: &p, ID: owned.AccountID, Revision: revision},
				fmt.Sprintf("%s %d\n%s", t.tr("Save custom configuration for Telegram ID", "ذخیرهٔ کانفیگ سفارشی برای شناسهٔ تلگرام"), id, freePlanSummary(p, t.tr)))
		}
		if kind == "planedit" {
			p.Revision++
		}
		if e = s.changeFreeCatalog(d.FreeRevision, func(c *freeCatalog) error {
			if kind == "plannew" {
				c.Plans = append(c.Plans, p)
			} else {
				c.Plans[slices.IndexFunc(c.Plans, func(plan freePlan) bool { return plan.ID == p.ID })] = p
			}
			return nil
		}); e != nil {
			return e
		}
		t.conversation = nil
		return t.freePlanMenu(ctx, "planview", p.ID)
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
		id, e := strconv.ParseInt(telegramDigits(text), 10, 64)
		if e != nil || id < 1 || id == t.config().ChatID {
			return errors.New("invalid Telegram ID")
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
		m, e := s.freeMember(id)
		if e != nil {
			return e
		}
		button := t.button("Block", "مسدودکردن", fmt.Sprintf("free:block:%d", id))
		if m.Blocked {
			button = t.button("Unblock", "رفع مسدودی", fmt.Sprintf("free:unblock:%d", id))
		}
		t.conversation = nil
		_, e = t.notify(ctx, t.config(), fmt.Sprintf("Telegram ID %d", id), telegramKeyboard{Rows: [][]telegramButton{{button, t.button("Back", "بازگشت", "free:manage")}}})
		return e
	case "broadcast":
		return t.previewFreeBroadcast(ctx, value, text)
	}
	return ErrNotFound
}
