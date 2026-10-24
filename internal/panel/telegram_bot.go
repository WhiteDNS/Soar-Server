package panel

import (
	"context"
	"errors"
	"fmt"
	"html"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type telegramButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}
type telegramKeyboard struct {
	Rows [][]telegramButton `json:"inline_keyboard"`
}
type telegramConversation struct {
	Key, ID, Field, Query string
	Input                 ClientInput
	Expires               time.Time
	Prompt                int64
	Creating              bool
}
type telegramConfirmation struct {
	Key, Action, ID, Version string
	Input                    ClientInput
	Revision                 uint64
	Expires                  time.Time
}

func (t *Telegram) tr(en, fa string) string {
	if t.config().Language == "fa" {
		return fa
	}
	return en
}
func (t *Telegram) button(en, fa, data string) telegramButton {
	return telegramButton{Text: t.tr(en, fa), Data: data}
}
func (t *Telegram) homeKeyboard() telegramKeyboard {
	return telegramKeyboard{Rows: [][]telegramButton{
		{t.button("👥 Users", "👥 کاربران", "users:0"), t.button("➕ Add user", "➕ افزودن کاربر", "new")},
		{t.button("📊 Server status", "📊 وضعیت سرور", "status"), t.button("🔄 Restart", "🔄 راه‌اندازی مجدد", "restart")},
		{t.button("⬆️ Updates", "⬆️ به‌روزرسانی", "updates"), t.button("⚙️ Preferences", "⚙️ ترجیحات", "prefs")},
	}}
}
func (t *Telegram) home(ctx context.Context) error {
	t.conversation = nil
	t.confirmation = nil
	_, e := t.notify(ctx, t.config(), t.tr("🛠️ WhiteDNS server management\nChoose an action.", "🛠️ مدیریت سرور WhiteDNS\nیک عملیات انتخاب کنید."), t.homeKeyboard())
	return e
}
func (t *Telegram) backKeyboard() telegramKeyboard {
	return telegramKeyboard{Rows: [][]telegramButton{{t.button("↩️ Back to menu", "↩️ بازگشت به منو", "home")}}}
}
func (t *Telegram) errorText(e error) string {
	if t.config().Language != "fa" {
		return e.Error()
	}
	switch {
	case errors.Is(e, ErrConflict):
		return "حساب هم‌زمان تغییر کرده است. دوباره آن را باز کنید."
	case errors.Is(e, ErrNotFound):
		return "حساب پیدا نشد. فهرست را تازه کنید."
	case strings.Contains(e.Error(), "confirmation"):
		return "تأیید منقضی یا قبلاً استفاده شده است. عملیات را دوباره انتخاب کنید."
	case strings.Contains(e.Error(), "invalid") || strings.Contains(e.Error(), "must") || strings.Contains(e.Error(), "required"):
		return "مقدار معتبر نیست؛ راهنمای فیلد را بررسی و دوباره تلاش کنید. (" + e.Error() + ")"
	default:
		return "عملیات انجام نشد؛ وضعیت را بررسی و دوباره تلاش کنید. (" + e.Error() + ")"
	}
}
func (t *Telegram) message(ctx context.Context, m telegramMessage) error {
	text := strings.TrimSpace(m.Text)
	switch strings.Split(text, " ")[0] {
	case "/start", "/menu", "/cancel":
		return t.home(ctx)
	case "/stats":
		return t.callback(ctx, "status")
	case "/help":
		_, e := t.notify(ctx, t.config(), t.tr("ℹ️ Manage users, view server status with /stats, restart the panel and install official stable updates using the buttons. Confirmations expire after five minutes. /cancel discards the current form. Only this private admin can act. Panel-wide settings remain in the dashboard.", "ℹ️ با دکمه‌ها کاربران را مدیریت کنید، با /stats وضعیت سرور را ببینید، سرویس را دوباره راه‌اندازی و نسخهٔ پایدار رسمی را نصب کنید. تأییدها پس از پنج دقیقه منقضی می‌شوند. /cancel فرم را لغو می‌کند. فقط مدیر خصوصی دسترسی دارد. تنظیمات کلی سرور در داشبورد است."), t.homeKeyboard())
		return e
	case "/settings":
		return t.preferences(ctx)
	}
	d := t.conversation
	if d == nil || d.Field == "" {
		return t.home(ctx)
	}
	if m.Reply == nil || m.Reply.ID != d.Prompt {
		return errors.New(t.tr("reply to the current prompt, or use /cancel", "به پیام پرسش فعلی پاسخ دهید یا /cancel را بفرستید"))
	}
	if d.Field == "search" {
		q := text
		t.conversation = nil
		return t.users(ctx, 0, q)
	}
	if e := t.setField(d, d.Field, text); e != nil {
		return e
	}
	field := d.Field
	d.Field = ""
	d.Prompt = 0
	if d.Creating && field == "name" {
		return t.chooseQuota(ctx)
	}
	if d.Creating && field == "quota" {
		return t.chooseExpiry(ctx)
	}
	return t.review(ctx)
}
func (t *Telegram) callback(ctx context.Context, data string) error {
	switch {
	case data == "home":
		return t.home(ctx)
	case data == "new":
		t.confirmation = nil
		t.conversation = &telegramConversation{Key: token()[:12], Input: ClientInput{Engines: "both", Enabled: true}, Expires: t.now().Add(15 * time.Minute), Creating: true}
		return t.prompt(ctx, "name")
	case data == "search":
		t.conversation = &telegramConversation{Key: token()[:12], Expires: t.now().Add(15 * time.Minute)}
		return t.prompt(ctx, "search")
	case strings.HasPrefix(data, "users:"):
		n, e := strconv.Atoi(strings.TrimPrefix(data, "users:"))
		if e != nil || n < 0 || n > MaxClients {
			return errors.New("invalid page")
		}
		return t.users(ctx, n, "")
	case strings.HasPrefix(data, "page:"):
		n, e := strconv.Atoi(strings.TrimPrefix(data, "page:"))
		if e != nil || n < 0 || n > MaxClients {
			return errors.New("invalid page")
		}
		q := ""
		if t.conversation != nil {
			q = t.conversation.Query
		}
		return t.users(ctx, n, q)
	case strings.HasPrefix(data, "user:"):
		return t.user(ctx, strings.TrimPrefix(data, "user:"))
	case strings.HasPrefix(data, "edit:"):
		c, e := t.h.store.Get(strings.TrimPrefix(data, "edit:"))
		if e != nil {
			return e
		}
		t.confirmation = nil
		t.conversation = &telegramConversation{Key: token()[:12], ID: c.ID, Input: accountInput(c), Expires: t.now().Add(15 * time.Minute)}
		return t.review(ctx)
	case strings.HasPrefix(data, "field:"):
		parts := strings.Split(data, ":")
		if len(parts) != 3 || t.conversation == nil || parts[1] != t.conversation.Key {
			return errors.New("form expired; reopen the account")
		}
		switch parts[2] {
		case "engines":
			return t.engineChoices(ctx)
		case "quota":
			return t.chooseQuota(ctx)
		case "expiry":
			return t.chooseExpiry(ctx)
		default:
			return t.prompt(ctx, parts[2])
		}
	case strings.HasPrefix(data, "set:"):
		p := strings.Split(data, ":")
		if len(p) != 4 || t.conversation == nil || p[1] != t.conversation.Key {
			return errors.New("form expired; reopen the account")
		}
		if p[3] == "custom" {
			return t.prompt(ctx, p[2])
		}
		if e := t.setField(t.conversation, p[2], p[3]); e != nil {
			return e
		}
		if t.conversation.Creating && p[2] == "quota" {
			return t.chooseExpiry(ctx)
		}
		return t.review(ctx)
	case strings.HasPrefix(data, "review:"):
		if t.conversation == nil || strings.TrimPrefix(data, "review:") != t.conversation.Key {
			return errors.New("form expired")
		}
		return t.review(ctx)
	case strings.HasPrefix(data, "save:"):
		d := t.conversation
		if d == nil || strings.TrimPrefix(data, "save:") != d.Key {
			return errors.New("form expired")
		}
		if e := d.Input.validate(); e != nil {
			return e
		}
		action := "edit"
		if d.Creating {
			action = "create"
		}
		return t.confirm(ctx, telegramConfirmation{Action: action, ID: d.ID, Input: d.Input, Revision: d.Input.Revision}, t.draftSummary(d))
	case strings.HasPrefix(data, "action:"):
		p := strings.Split(data, ":")
		if len(p) != 3 {
			return errors.New("invalid action")
		}
		c, e := t.h.store.Get(p[2])
		if e != nil {
			return e
		}
		desc := map[string]string{"toggle": t.tr("Change enabled state. Disabling closes all active connections.", "تغییر وضعیت حساب. غیرفعال‌سازی همهٔ اتصال‌ها را می‌بندد."), "delete": t.tr("Permanently delete this account, usage, keys and subscription.", "حذف دائمی حساب، مصرف، کلیدها و ساب."), "reset": t.tr("Reset usage to zero and close connections. This cannot be undone.", "مصرف صفر و اتصال‌ها بسته می‌شوند. برگشت‌پذیر نیست."), "disconnect": t.tr("Close current connections. An active account can reconnect.", "اتصال‌های فعلی بسته می‌شوند. حساب فعال می‌تواند دوباره وصل شود."), "rotate": t.tr("Replace tunnel keys; old direct links stop working. Usage is preserved.", "تعویض کلید تونل؛ لینک مستقیم قبلی باطل می‌شود. مصرف حفظ می‌شود."), "subscription-rotate": t.tr("Revoke the old subscription URL. Cached tunnel keys remain valid.", "لینک ساب قبلی باطل می‌شود؛ کلیدهای واردشده در گوشی معتبر می‌مانند.")}[p[1]]
		if desc == "" {
			return errors.New("invalid action")
		}
		action := p[1]
		in := accountInput(c)
		if action == "toggle" {
			action = "edit"
			in.Enabled = !in.Enabled
		}
		return t.confirm(ctx, telegramConfirmation{Action: action, ID: c.ID, Revision: c.Revision, Input: in}, c.Name+"\n"+desc)
	case strings.HasPrefix(data, "links:"):
		return t.links(ctx, strings.TrimPrefix(data, "links:"))
	case strings.HasPrefix(data, "ok:"):
		return t.execute(ctx, strings.TrimPrefix(data, "ok:"))
	case data == "status":
		text, e := t.report()
		if e != nil {
			return e
		}
		_, e = t.notify(ctx, t.config(), text, t.backKeyboard())
		return e
	case data == "restart":
		return t.confirm(ctx, telegramConfirmation{Action: "restart"}, t.tr("Restart the panel and all Soar/Cotten runtimes? Active connections will drop briefly.", "پنل و موتورهای Soar/Cotten دوباره راه‌اندازی شوند؟ اتصال‌های فعال موقتاً قطع می‌شوند."))
	case data == "updates":
		return t.updates(ctx)
	case data == "prefs":
		return t.preferences(ctx)
	case strings.HasPrefix(data, "pref:"):
		return t.preference(ctx, strings.TrimPrefix(data, "pref:"))
	}
	return errors.New("unknown menu action; use /menu")
}
func (t *Telegram) prompt(ctx context.Context, field string) error {
	prompts := map[string]string{
		"name":      t.tr("Reply with the account name (max 160 bytes).", "نام حساب را در پاسخ بفرستید (حداکثر ۱۶۰ بایت)."),
		"note":      t.tr("Reply with a note (max 2048 bytes); send - to clear.", "یادداشت را در پاسخ بفرستید (حداکثر ۲۰۴۸ بایت)؛ برای حذف - بفرستید."),
		"quota":     t.tr("Reply with quota in GiB; 0 means unlimited.", "حجم را به GiB در پاسخ بفرستید؛ ۰ یعنی نامحدود."),
		"expiry":    t.tr("Reply with expiry: YYYY-MM-DD HH:MM (Gregorian, "+t.config().Timezone+"); 0 means no expiry.", "انقضا را با تاریخ میلادی YYYY-MM-DD HH:MM در منطقهٔ "+t.config().Timezone+" بفرستید؛ ۰ یعنی بدون انقضا."),
		"rate":      t.tr("Reply with speed in KiB/s; 0 means unlimited.", "سرعت را به KiB/s در پاسخ بفرستید؛ ۰ یعنی نامحدود."),
		"streams":   t.tr("Reply with TCP stream limit (0–2147483647); 0 means unlimited, not device count.", "سقف جریان TCP را بفرستید (۰ تا ۲۱۴۷۴۸۳۶۴۷)؛ ۰ نامحدود است. تعداد دستگاه نیست."),
		"soar":      t.tr("Reply with Soar session limit (1–32768).", "سقف نشست Soar را بفرستید (۱ تا ۳۲۷۶۸)."),
		"masterdns": t.tr("Reply with MasterDNS session limit (1–255).", "سقف نشست MasterDNS را بفرستید (۱ تا ۲۵۵)."),
		"cotten":    t.tr("Reply with Cotten session limit (1–65535).", "سقف نشست Cotten را بفرستید (۱ تا ۶۵۵۳۵)."),
		"search":    t.tr("Reply with a name, note or domain to search.", "برای جست‌وجو، نام، یادداشت یا دامنه را در پاسخ بفرستید."),
	}
	text, ok := prompts[field]
	if !ok {
		return errors.New("invalid form field")
	}
	id, e := t.notify(ctx, t.config(), text, map[string]any{"force_reply": true, "selective": true})
	if e == nil {
		t.conversation.Field = field
		t.conversation.Prompt = id
	}
	return e
}
func telegramDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '۰' && r <= '۹' {
			return '0' + r - '۰'
		}
		if r >= '٠' && r <= '٩' {
			return '0' + r - '٠'
		}
		return r
	}, strings.TrimSpace(s))
}
func (t *Telegram) setField(d *telegramConversation, field, value string) error {
	v := telegramDigits(value)
	in := d.Input
	switch field {
	case "name":
		in.Name = strings.TrimSpace(value)
		if in.Name == "" || len(in.Name) > 160 {
			return errors.New("name required (max 160 bytes)")
		}
	case "note":
		if len(value) > 2048 {
			return errors.New("note must be at most 2048 bytes")
		}
		in.Note = value
		if value == "-" {
			in.Note = ""
		}
	case "engines":
		if !validEngines(v) || v == "" {
			return errors.New("invalid engines")
		}
		in.Engines = v
	case "quota", "rate":
		n, e := strconv.ParseFloat(v, 64)
		scale, limit := float64(1<<30), float64(1<<60)
		if field == "rate" {
			scale, limit = 1024, float64(1<<30)
		}
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n*scale > limit {
			return errors.New("invalid quota or speed")
		}
		n = math.Round(n * scale)
		if field == "quota" {
			in.QuotaBytes = int64(n)
		} else {
			in.RateBytes = int64(n)
		}
	case "expiry":
		if v == "0" {
			in.ExpiresAt = 0
		} else if strings.HasSuffix(v, "d") {
			n, e := strconv.Atoi(strings.TrimSuffix(v, "d"))
			if e != nil || n < 1 || n > 3650 {
				return errors.New("invalid expiry")
			}
			in.ExpiresAt = t.now().Add(time.Duration(n) * 24 * time.Hour).UnixMilli()
		} else {
			loc, _ := time.LoadLocation(t.config().Timezone)
			date, e := time.ParseInLocation("2006-01-02 15:04", v, loc)
			if e != nil || !date.After(t.now()) {
				return errors.New("expiry must be a future Gregorian date: YYYY-MM-DD HH:MM")
			}
			in.ExpiresAt = date.UnixMilli()
		}
	case "streams", "soar", "cotten", "masterdns":
		n, e := strconv.ParseInt(v, 10, 32)
		limit := int64(MaxStreamLimit)
		minimum := int64(0)
		if field == "soar" {
			minimum = 1
			limit = MaxSoarSessionLimit
		}
		if field == "masterdns" {
			minimum = 1
			limit = MaxMasterDNSSessionLimit
		}
		if field == "cotten" {
			minimum = 1
			limit = MaxCottenSessionLimit
		}
		if e != nil || n < minimum || n > limit {
			return errors.New("invalid connection limit")
		}
		num := int(n)
		switch field {
		case "streams":
			in.StreamLimit = num
		case "soar":
			in.SoarSessionLimit = &num
		case "masterdns":
			in.MasterDNSSessionLimit = &num
		case "cotten":
			in.CottenSessionLimit = &num
		}
	default:
		return errors.New("invalid field")
	}
	d.Input = in
	d.Field = ""
	d.Prompt = 0
	return nil
}
func (t *Telegram) chooseQuota(ctx context.Context) error {
	d := t.conversation
	if d == nil {
		return errors.New("form expired")
	}
	d.Field = ""
	p := "set:" + d.Key + ":quota:"
	k := telegramKeyboard{Rows: [][]telegramButton{{t.button("1 GiB", "۱ GiB", p+"1"), t.button("5 GiB", "۵ GiB", p+"5"), t.button("10 GiB", "۱۰ GiB", p+"10"), t.button("20 GiB", "۲۰ GiB", p+"20")}, {t.button("Unlimited", "نامحدود", p+"0"), t.button("Custom", "دلخواه", p+"custom")}, {t.button("❌ Cancel", "❌ لغو", "home")}}}
	_, e := t.notify(ctx, t.config(), t.tr("📦 Choose account quota.", "📦 حجم حساب را انتخاب کنید."), k)
	return e
}
func (t *Telegram) chooseExpiry(ctx context.Context) error {
	d := t.conversation
	if d == nil {
		return errors.New("form expired")
	}
	d.Field = ""
	p := "set:" + d.Key + ":expiry:"
	k := telegramKeyboard{Rows: [][]telegramButton{{t.button("7 days", "۷ روز", p+"7d"), t.button("30 days", "۳۰ روز", p+"30d"), t.button("90 days", "۹۰ روز", p+"90d")}, {t.button("No expiry", "بدون انقضا", p+"0"), t.button("Custom", "دلخواه", p+"custom")}, {t.button("❌ Cancel", "❌ لغو", "home")}}}
	_, e := t.notify(ctx, t.config(), t.tr("📅 Choose account expiry.", "📅 انقضای حساب را انتخاب کنید."), k)
	return e
}
func (t *Telegram) engineChoices(ctx context.Context) error {
	d := t.conversation
	p := "set:" + d.Key + ":engines:"
	_, e := t.notify(ctx, t.config(), t.tr("🔀 Choose connection engines.", "🔀 موتور اتصال را انتخاب کنید."), telegramKeyboard{Rows: [][]telegramButton{{t.button("Both", "هر دو", p+"both"), t.button("Soar", "Soar", p+"soar"), t.button("Cotten", "Cotten", p+"cotten")}, {t.button("MasterDNS", "MasterDNS", p+"masterdns"), t.button("All three", "هر سه موتور", p+"all")}, {t.button("Soar + MasterDNS", "Soar + MasterDNS", p+"soar_masterdns"), t.button("Cotten + MasterDNS", "Cotten + MasterDNS", p+"cotten_masterdns")}}})
	return e
}
func (t *Telegram) draftSummary(d *telegramConversation) string {
	in := d.Input
	soar, cotten := DefaultSoarSessionLimit, DefaultCottenSessionLimit
	master := DefaultMasterDNSSessionLimit
	if in.MasterDNSSessionLimit != nil {
		master = *in.MasterDNSSessionLimit
	}
	if in.SoarSessionLimit != nil {
		soar = *in.SoarSessionLimit
	}
	if in.CottenSessionLimit != nil {
		cotten = *in.CottenSessionLimit
	}
	expiry := t.tr("No expiry", "بدون انقضا")
	if in.ExpiresAt > 0 {
		loc, _ := time.LoadLocation(t.config().Timezone)
		expiry = time.UnixMilli(in.ExpiresAt).In(loc).Format("2006-01-02 15:04") + " " + loc.String()
	}
	return fmt.Sprintf(t.tr("📝 Review account\nName: %s\nEngines: %s\nEnabled: %t\nQuota: %.2f GiB (0 = unlimited)\nExpiry: %s\nSpeed: %.2f KiB/s (0 = unlimited)\nTCP streams: %d (0 = unlimited)\nSessions Soar/Cotten/MasterDNS: %d / %d / %d\nNote: %s", "📝 بررسی حساب\nنام: %s\nموتور: %s\nفعال: %t\nحجم: %.2f GiB (۰ نامحدود)\nانقضا: %s\nسرعت: %.2f KiB/s (۰ نامحدود)\nجریان TCP: %d (۰ نامحدود)\nنشست Soar/Cotten/MasterDNS: %d / %d / %d\nیادداشت: %s"), in.Name, in.Engines, in.Enabled, float64(in.QuotaBytes)/(1<<30), expiry, float64(in.RateBytes)/1024, in.StreamLimit, soar, cotten, master, in.Note)
}
func (t *Telegram) review(ctx context.Context) error {
	d := t.conversation
	if d == nil {
		return errors.New("form expired")
	}
	d.Field = ""
	d.Prompt = 0
	t.confirmation = nil
	p := "field:" + d.Key + ":"
	k := telegramKeyboard{Rows: [][]telegramButton{
		{t.button("Name", "نام", p+"name"), t.button("Quota", "حجم", p+"quota"), t.button("Expiry", "انقضا", p+"expiry")},
		{t.button("Engines", "موتور", p+"engines"), t.button("Note", "یادداشت", p+"note"), t.button("Speed", "سرعت", p+"rate")},
		{t.button("TCP streams", "جریان TCP", p+"streams"), t.button("Soar sessions", "نشست Soar", p+"soar"), t.button("Cotten sessions", "نشست Cotten", p+"cotten")},
		{t.button("MasterDNS sessions", "نشست MasterDNS", p+"masterdns")},
		{t.button("💾 Save", "💾 ذخیره", "save:"+d.Key), t.button("❌ Cancel", "❌ لغو", "home")}}}
	_, e := t.notify(ctx, t.config(), t.draftSummary(d), k)
	return e
}
func (t *Telegram) users(ctx context.Context, page int, query string) error {
	list, e := t.h.store.List()
	if e != nil {
		return e
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt > list[j].CreatedAt })
	filtered := []Client{}
	q := strings.ToLower(query)
	for _, c := range list {
		if !c.InstallerProbe && (q == "" || strings.Contains(strings.ToLower(c.Name+" "+c.Note+" "+c.Zone), q)) {
			filtered = append(filtered, c)
		}
	}
	t.conversation = &telegramConversation{Query: query, Expires: t.now().Add(15 * time.Minute)}
	t.confirmation = nil
	pages := max(1, (len(filtered)+7)/8)
	page = min(page, pages-1)
	k := telegramKeyboard{}
	for _, c := range filtered[min(page*8, len(filtered)):min((page+1)*8, len(filtered))] {
		name := []rune(c.Name)
		if len(name) > 35 {
			name = name[:35]
		}
		k.Rows = append(k.Rows, []telegramButton{t.button(string(name)+" · "+t.accountStatus(c), string(name)+" · "+t.accountStatus(c), "user:"+c.ID)})
	}
	nav := []telegramButton{}
	if page > 0 {
		nav = append(nav, t.button("Previous", "قبلی", fmt.Sprintf("page:%d", page-1)))
	}
	if page+1 < pages {
		nav = append(nav, t.button("Next", "بعدی", fmt.Sprintf("page:%d", page+1)))
	}
	if len(nav) > 0 {
		k.Rows = append(k.Rows, nav)
	}
	k.Rows = append(k.Rows, []telegramButton{t.button("🔍 Search", "🔍 جست‌وجو", "search"), t.button("➕ Add user", "➕ افزودن کاربر", "new"), t.button("↩️ Menu", "↩️ منو", "home")})
	_, e = t.notify(ctx, t.config(), fmt.Sprintf(t.tr("👥 Users: %d · Page %d/%d", "👥 کاربران: %d · صفحهٔ %d/%d"), len(filtered), page+1, pages), k)
	return e
}
func (t *Telegram) accountStatus(c Client) string {
	s := c.Status(t.now())
	if t.config().Language == "fa" {
		return map[string]string{"active": "فعال", "disabled": "غیرفعال", "expired": "منقضی", "exhausted": "حجم تمام‌شده"}[s]
	}
	return s
}
func (t *Telegram) user(ctx context.Context, id string) error {
	c, e := t.h.store.Get(id)
	if e != nil {
		return e
	}
	t.conversation = nil
	t.confirmation = nil
	d := &telegramConversation{Input: accountInput(c)}
	k := telegramKeyboard{Rows: [][]telegramButton{
		{t.button("✏️ Edit", "✏️ ویرایش", "edit:"+id), t.button("🔗 Connection strings", "🔗 رشته‌های اتصال", "links:"+id)},
		{t.button("Enable / Disable", "فعال / غیرفعال", "action:toggle:"+id), t.button("🔌 Disconnect", "🔌 قطع اتصال", "action:disconnect:"+id)},
		{t.button("♻️ Reset usage", "♻️ صفر کردن مصرف", "action:reset:"+id), t.button("🔑 Rotate tunnel key", "🔑 تعویض کلید تونل", "action:rotate:"+id)},
		{t.button("🔗 Rotate subscription URL", "🔗 تعویض لینک ساب", "action:subscription-rotate:"+id)},
		{t.button("🗑️ Delete", "🗑️ حذف", "action:delete:"+id), t.button("👥 Users", "👥 کاربران", "users:0")}}}
	counts := t.h.manager.ActiveCounts()
	_, e = t.notify(ctx, t.config(), t.draftSummary(d)+fmt.Sprintf(t.tr("\nStatus: %s\nCurrent usage: %.2f GiB\nActive TCP streams: %d", "\nوضعیت: %s\nمصرف فعلی: %.2f GiB\nجریان TCP فعال: %d"), t.accountStatus(c), float64(c.Used())/(1<<30), counts[id]), k)
	return e
}
func (t *Telegram) confirm(ctx context.Context, c telegramConfirmation, text string) error {
	c.Key = token()[:12]
	c.Expires = t.now().Add(5 * time.Minute)
	t.confirmation = &c
	_, e := t.notify(ctx, t.config(), text+"\n"+t.tr("⚠️ Confirm within five minutes.", "⚠️ تا پنج دقیقه تأیید کنید."), telegramKeyboard{Rows: [][]telegramButton{{t.button("✅ Confirm", "✅ تأیید", "ok:"+c.Key), t.button("❌ Cancel", "❌ لغو", "home")}}})
	return e
}
func (t *Telegram) execute(ctx context.Context, key string) error {
	c := t.confirmation
	t.confirmation = nil
	if c == nil || c.Key != key || !t.now().Before(c.Expires) {
		return errors.New("confirmation expired or already used")
	}
	if t.state.PendingAction != "" {
		return errors.New("another action is pending; check its result before retrying")
	}
	t.conversation = nil
	t.state.PendingAction = c.Action
	t.state.PendingSince = t.now().UnixMilli()
	t.state.PendingJob = ""
	if e := t.save(); e != nil {
		return errors.New("cannot persist action; nothing was executed")
	}
	if c.Action == "restart" || c.Action == "update" {
		if t.h.ops == nil {
			return t.finishAction(errors.New("maintenance service unavailable; rerun the current panel installer"))
		}
		if c.Action == "update" {
			// Revalidate the selected official stable release immediately before dispatch.
			r, e := t.releaseVersion(ctx, c.Version)
			if e != nil {
				return t.finishAction(e)
			}
			if r.Prerelease || strings.Contains(c.Version, "-") || !NewerVersion(c.Version, Version) {
				return t.finishAction(errors.New("choose a newer official stable release"))
			}
		}
		job, e := t.h.ops.Start(ctx, MaintenanceRequest{Action: c.Action, Version: c.Version})
		if e != nil {
			// A socket error may follow acceptance; keep the marker for status recovery.
			return errors.New("maintenance result is uncertain; check server status before retrying")
		}
		_ = t.h.store.Audit("maintenance."+c.Action+"_requested", "")
		t.state.PendingJob = job.ID
		if e = t.save(); e != nil {
			return errors.New("maintenance accepted; result tracking interrupted, check server status")
		}
		_, e = t.notify(ctx, t.config(), t.tr("⏳ Maintenance accepted. Connections may drop briefly. I will report the result when the panel returns.", "⏳ درخواست نگهداری ثبت شد. اتصال‌ها ممکن است موقتاً قطع شوند. پس از بازگشت پنل نتیجه را می‌فرستم."), t.backKeyboard())
		return e
	}
	changed, e := t.h.applyAccountAction(c.Action, c.ID, c.Input, &c.Revision)
	if c.Action == "create" && e == nil {
		c.ID = changed.ID
	}
	if e = t.finishAction(e); e != nil {
		return e
	}
	_ = t.h.store.Audit("telegram.action_completed", c.ID)
	if c.Action == "create" {
		_, e = t.notify(ctx, t.config(), t.tr("✅ Account created. Open Users to copy its connection strings.", "✅ حساب ساخته شد. رشته‌های اتصال آن را از کاربران کپی کنید."), t.homeKeyboard())
		return e
	}
	_, e = t.notify(ctx, t.config(), t.tr("✅ Action completed.", "✅ عملیات انجام شد."), t.homeKeyboard())
	return e
}
func (t *Telegram) finishAction(err error) error {
	t.state.PendingAction = ""
	t.state.PendingJob = ""
	t.state.PendingSince = 0
	if e := t.save(); e != nil {
		return errors.New("action result could not be persisted; check current state before retrying")
	}
	return err
}
func (t *Telegram) links(ctx context.Context, id string) error {
	c := t.config()
	links, e := t.h.store.Profiles(id)
	if e != nil {
		return e
	}
	credential, e := t.h.store.SubscriptionToken(id, false)
	if e != nil {
		return e
	}
	origin := c.Origin
	if t.h.access != nil && t.h.access.Config().HTTPS {
		origin = "https://" + t.h.access.Config().Domain
	}
	u, e := url.Parse(origin)
	if e != nil || u.Host == "" {
		return errors.New("save Telegram settings in the dashboard to establish the subscription address")
	}
	u.Path = "/sub/" + credential
	u.RawQuery = ""
	u.Fragment = ""
	type share struct{ name, link string }
	all := []share{{t.tr("Subscription", "ساب"), u.String()}}
	for _, engine := range profileEngines {
		if link := links[engine]; link != "" {
			all = append(all, share{engine, link})
		}
	}
	for _, s := range all {
		text := html.EscapeString(s.name) + "\n<pre>" + html.EscapeString(s.link) + "</pre>"
		e = t.call(ctx, c, "sendMessage", map[string]any{
			"chat_id": c.ChatID, "text": text, "parse_mode": "HTML",
			"link_preview_options": map[string]bool{"is_disabled": true},
		}, nil)
		t.connection(e)
		if e != nil {
			return e
		}
	}
	return nil
}
func (t *Telegram) updates(ctx context.Context) error {
	r, e := t.latest(ctx)
	if e != nil {
		return e
	}
	v := strings.TrimPrefix(r.Tag, "panel-v")
	if !NewerVersion(v, Version) {
		_, e = t.notify(ctx, t.config(), t.tr("✅ The installed panel is up to date: ", "✅ نسخهٔ نصب‌شده به‌روز است: ")+Version, t.backKeyboard())
		return e
	}
	if r.Prerelease || strings.Contains(v, "-") || validatePanelRelease(r, v) != nil {
		return errors.New("invalid official stable release")
	}
	return t.confirm(ctx, telegramConfirmation{Action: "update", Version: v}, fmt.Sprintf(t.tr("Install official stable panel %s → %s?\nThe bundle is verified and backed up; activation failure triggers rollback. Connections will briefly drop.\n%s", "نسخهٔ پایدار رسمی پنل %s ← %s نصب شود؟\nبسته بررسی و پشتیبان گرفته می‌شود؛ در شکست فعال‌سازی نسخهٔ قبلی برمی‌گردد. اتصال‌ها موقتاً قطع می‌شوند.\n%s"), Version, v, r.URL))
}
func (t *Telegram) preferences(ctx context.Context) error {
	c := t.config()
	_, e := t.notify(ctx, c, fmt.Sprintf(t.tr("⚙️ Preferences\nLanguage: %s\nAlerts: %t\nDaily report: %t at %s (%s)\nChange report time and timezone in dashboard settings.", "⚙️ ترجیحات\nزبان: %s\nهشدار: %t\nگزارش روزانه: %t ساعت %s (%s)\nزمان و منطقهٔ زمانی گزارش را در تنظیمات داشبورد تغییر دهید."), c.Language, c.Alerts, c.DailyReport, c.ReportTime, c.Timezone), telegramKeyboard{Rows: [][]telegramButton{{t.button("فارسی / English", "فارسی / English", "pref:language")}, {t.button("Toggle alerts", "تغییر وضعیت هشدار", "pref:alerts"), t.button("Toggle daily report", "تغییر گزارش روزانه", "pref:report")}, {t.button("↩️ Menu", "↩️ منو", "home")}}})
	return e
}
func (t *Telegram) preference(ctx context.Context, field string) error {
	old := t.config()
	started := t.state.ReportStarted
	c := old
	switch field {
	case "language":
		c.Language = "fa"
		if old.Language == "fa" {
			c.Language = "en"
		}
	case "alerts":
		c.Alerts = !c.Alerts
	case "report":
		c.DailyReport = !c.DailyReport
		if c.DailyReport && t.state.ReportStarted == 0 {
			t.state.ReportStarted = t.now().UnixMilli()
		}
	default:
		return errors.New("invalid preference")
	}
	t.mu.Lock()
	t.cfg = c
	t.mu.Unlock()
	if e := t.save(); e != nil {
		t.state.ReportStarted = started
		t.mu.Lock()
		t.cfg = old
		t.mu.Unlock()
		return errors.New("cannot save preference")
	}
	return t.preferences(ctx)
}
