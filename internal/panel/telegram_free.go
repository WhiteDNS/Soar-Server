package panel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	freeTrafficLimit      = 12
	freeChallengeLifetime = 5 * time.Minute
)

type telegramFreeChallenge struct {
	Code    string
	Prompt  int64
	Expires time.Time
	Plan    freePlan
}

func freeAction(u telegramUpdate) (string, string) {
	if u.Callback != nil {
		p, ok := strings.CutPrefix(u.Callback.Data, "free:")
		if !ok {
			return "", ""
		}
		action, value, _ := strings.Cut(p, ":")
		switch action {
		case "home":
			return "menu", ""
		case "request", "status", "config", "qr", "link", "plan", "language", "lang", "guide", "terms", "cancel", "opt":
			return action, value
		}
		return "", ""
	}
	if u.Message == nil {
		return "", ""
	}
	switch strings.TrimSpace(u.Message.Text) {
	case "/start", "/start free", "/menu":
		return "menu", ""
	case "/free":
		return "request", ""
	case "/free_status":
		return "status", ""
	case "/free_config":
		return "config", ""
	case "/free_qr":
		return "qr", ""
	case "/language":
		return "language", ""
	case "/help":
		return "guide", ""
	case "/cancel":
		return "cancel", ""
	}
	if u.Message.Reply != nil && len(u.Message.Text) <= 1000 {
		return "reply", strings.TrimSpace(u.Message.Text)
	}
	return "", ""
}

func (t *Telegram) handleFreeUpdate(ctx context.Context, c TelegramConfig, from telegramUser, u telegramUpdate) {
	now := t.now()
	action, value := freeAction(u)
	if action == "" {
		return
	}
	t.freeMu.Lock()
	challenge := t.freeChallenges[from.ID]
	t.freeMu.Unlock()
	if action == "reply" && (challenge.Prompt == 0 || u.Message.Reply.ID != challenge.Prompt || !now.Before(challenge.Expires)) {
		return
	}
	store := t.h.store
	catalog, err := store.freeCatalog()
	if err != nil {
		t.freeTraffic.failed.Add(1)
		return
	}
	limits := catalog.limits()
	owned := freeReadAction(action) && t.h.store.hasFreeOwner(from.ID)
	menu := action == "request" && u.Callback != nil || oneOf(action, "menu", "status", "guide", "terms", "language")
	lane, _ := ctx.Value(freeLaneKey{}).(freeLane)
	queuedReply := lane == freeAdmissionLane && action == "reply"
	if lane != freeMenuLane && !queuedReply && !t.allowFreeJob(from.ID, now, action, owned, menu, limits) {
		revision, _ := ctx.Value(freePublicConfigKey{}).(uint64)
		t.rejectFreeJob(telegramFreeJob{config: c, revision: revision, from: from})
		return
	}
	if lane != freeMenuLane {
		ctx = withFreeLane(ctx, freeAdmissionLane)
		if owned {
			ctx = withFreeLane(ctx, freeOwnerLane)
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	c.ChatID = from.ID
	if u.Callback != nil {
		ctx = withTelegramMenu(ctx, c.ChatID, u.Callback.Message.ID)
	}
	member, err := store.freeMember(from.ID)
	if err != nil {
		t.freeTraffic.failed.Add(1)
		return
	}
	language := member.Language
	if language == "" {
		language = "en"
		if strings.HasPrefix(from.Language, "fa") {
			language = "fa"
		}
	}
	tr := func(en, fa string) string {
		if language == "fa" {
			return fa
		}
		return en
	}
	button := func(en, fa, data string) telegramButton {
		return telegramButton{Text: tr(en, fa), Data: "free:" + data}
	}
	languageButton := telegramButton{Text: telegramLanguageLabel, Data: "free:language"}
	back := []telegramButton{button("Main menu", "بازگشت به منوی اصلی", "home")}
	send := func(en, fa string, markup any) int64 {
		// A blocked customer chat must not mark the management bot disconnected.
		live := t.config()
		if !live.Enabled || !live.FreeAccess || live.Token != c.Token {
			return 0
		}
		if markup == nil {
			markup = telegramKeyboard{Rows: [][]telegramButton{back}}
		} else if keyboard, ok := markup.(telegramKeyboard); ok && action != "menu" {
			keyboard.Rows = append(keyboard.Rows, back)
			markup = keyboard
		}
		id, err := t.sendText(ctx, c, tr(en, fa), markup)
		if err == nil {
			t.freeTraffic.sent.Add(1)
		} else {
			t.freeTraffic.failed.Add(1)
		}
		return id
	}
	if u.Callback != nil {
		defer func() {
			_ = t.call(ctx, c, "answerCallbackQuery", map[string]string{"callback_query_id": u.Callback.ID}, nil)
		}()
	}
	if member.Blocked && action != "language" && action != "lang" {
		send("The administrator has restricted this Telegram account. Ask the service administrator to review your access.",
			"دسترسی این شناسه به‌دست مدیر محدود شده است. برای بازبینی دسترسی با مسئول سرویس تماس بگیرید.",
			telegramKeyboard{Rows: [][]telegramButton{{languageButton}}})
		return
	}
	if catalog.RestoreHold && action != "language" && action != "lang" {
		send("Public service is paused while the administrator reviews the restored backup.",
			"سرویس عمومی تا پایان بررسی بکاپ بازیابی‌شده متوقف است.",
			telegramKeyboard{Rows: [][]telegramButton{{languageButton}}})
		return
	}
	if action == "menu" {
		if !member.Started || member.Language == "" || now.Sub(member.Seen) >= time.Minute {
			_ = store.updateFreeMember(from.ID, now, func(m *freeMember) {
				m.Started = true
				if m.Language == "" {
					m.Language = language
				}
			})
		}
		welcome := catalog.Text["welcome:"+language]
		if welcome == "" {
			welcome = tr("To get a free configuration, choose a plan and reply to the verification message. Follow the decision in My account.", "برای دریافت کانفیگ رایگان، یک پلن انتخاب کنید و به پیام بررسی پاسخ بدهید. نتیجه را در «حساب من» ببینید.")
		}
		send(welcome, welcome, telegramKeyboard{Rows: [][]telegramButton{
			{languageButton},
			{button("Get a free configuration", "دریافت کانفیگ رایگان", "request")},
			{button("My account", "حساب من", "status")},
			{button("Help", "راهنما", "guide")},
		}})
		return
	}
	switch action {
	case "language":
		title := "زبان را انتخاب کنید / Choose your language"
		send(title, title, telegramKeyboard{Rows: [][]telegramButton{
			{{Text: "فارسی", Data: "free:lang:fa"}, {Text: "English", Data: "free:lang:en"}},
			{button("Enable announcements", "دریافت اعلان‌ها", "opt:on"), button("Turn off announcements", "قطع اعلان‌ها", "opt:off")},
		}})
		return
	case "lang", "opt":
		if (action == "lang" && (value == "en" || value == "fa")) || (action == "opt" && (value == "on" || value == "off")) {
			if e := store.updateFreeMember(from.ID, now, func(m *freeMember) {
				if action == "lang" {
					m.Language = value
				} else {
					m.OptOut = value == "off"
				}
			}); e == nil {
				if action == "lang" {
					language = value
					back = []telegramButton{button("Main menu", "بازگشت به منوی اصلی", "home")}
				}
				send("Preference saved.", "تنظیمات ذخیره شد.", nil)
			}
		}
		return
	case "terms", "guide":
		body := catalog.Text[action+":"+language]
		if body == "" {
			if action == "terms" {
				body = tr("Plans are free. One account per Telegram ID. Requests are reviewed manually. Keep your connection link private.",
					"پلن‌ها رایگان‌اند. هر شناسهٔ تلگرام یک حساب دارد. درخواست‌ها دستی بررسی می‌شوند. لینک اتصال را خصوصی نگه دارید.")
			} else {
				body = tr("Open My account, choose a link or QR code, then import the profile into WhiteDNS.", "«حساب من» را باز کنید و لینک یا کد QR را بگیرید. سپس کانفیگ را در WhiteDNS وارد کنید.")
			}
		}
		if action == "guide" {
			send(body, body, telegramKeyboard{Rows: [][]telegramButton{{button("Terms of use", "شرایط استفاده", "terms")}}})
		} else {
			send(body, body, nil)
		}
		return
	case "cancel":
		t.clearFreeChallenge(from.ID)
		if cancelled, e := store.cancelFree(from.ID, now); e == nil && cancelled {
			send(fmt.Sprintf("You cancelled your pending request. You can apply again in %d hours.", limits.RetryHours),
				telegramPersianDigits.Replace(fmt.Sprintf("درخواست شما لغو شد. %d ساعت دیگر می‌توانید دوباره درخواست بدهید.", limits.RetryHours)), nil)
		} else if e == nil {
			send("There is no pending request to cancel. Open My account to see your current status.", "درخواست بازی برای لغو ندارید. وضعیت را در «حساب من» ببینید.", nil)
		}
		return
	}
	req, err := store.freeRequest(c.ChatID, now)
	if err != nil {
		send("Service unavailable. Try again later.", "سرویس موقتاً در دسترس نیست. بعداً امتحان کنید.", nil)
		return
	}
	retryEN, retryFA := freeRetryDate(req.retryAt(limits))
	var account Client
	if req.AccountID != "" {
		account, err = store.Get(req.AccountID)
		if errors.Is(err, ErrNotFound) && !req.RetryAllowed {
			send("Your previous configuration is no longer available. You can request a new one from "+retryEN+".",
				"کانفیگ قبلی دیگر در پنل موجود نیست. از "+retryFA+" می‌توانید دوباره درخواست بدهید.", nil)
			return
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			send("We could not read your account. Open My account again in a moment.", "خواندن حساب فعلاً ممکن نیست. کمی بعد دوباره «حساب من» را باز کنید.", nil)
			return
		}
	}
	if account.ID != "" {
		status := account.Status(now)
		if status == "active" && (action == "config" || action == "link" || action == "qr") {
			if !t.freeAccountReady(account) {
				send("Your account is being prepared. Check your status again shortly.", "حساب در حال آماده‌سازی است. کمی بعد وضعیت را دوباره بررسی کنید.", nil)
				return
			}
			engines := engineSelections[account.Engines]
			if value == "" && len(engines) > 1 {
				kind := "link"
				if action == "qr" {
					kind = "qr"
				}
				k := telegramKeyboard{}
				for _, engine := range engines {
					k.Rows = append(k.Rows, []telegramButton{{Text: freeEngineName(engine), Data: "free:" + kind + ":" + engine}})
				}
				k.Rows = append(k.Rows, []telegramButton{button("My account", "بازگشت به حساب من", "status")})
				send("Choose an engine.", "موتور را انتخاب کنید.", k)
				return
			}
			links, e := store.Profiles(account.ID)
			var link string
			if e == nil {
				link, e = selectedProfile(links, value)
			}
			if e == nil && !t.canDeliverFree(c, from.ID, account) {
				return
			}
			if e == nil && action == "qr" {
				e = t.freeQR(ctx, c, link)
			}
			if e != nil {
				send("Configuration unavailable. Try again later.", "کانفیگ در دسترس نیست. بعداً امتحان کنید.", nil)
			} else if action != "qr" {
				_, _ = t.sendText(ctx, c, link, nil)
			}
			return
		}
		statusFA := map[string]string{"active": "فعال", "disabled": "غیرفعال", "expired": "منقضی", "exhausted": "حجم تمام‌شده"}[status]
		if status == "active" && !t.freeAccountReady(account) {
			status, statusFA = "Preparing", "در حال آماده‌سازی"
		}
		usage := float64(account.Used()) / (1 << 20)
		leftEN, leftFA := "Unlimited", "نامحدود"
		if account.QuotaBytes > 0 {
			leftEN = fmt.Sprintf("%.1f MiB", float64(max(0, account.QuotaBytes-account.Used()))/(1<<20))
			leftFA = telegramPersianDigits.Replace(fmt.Sprintf("%.1f میبی‌بایت", float64(max(0, account.QuotaBytes-account.Used()))/(1<<20)))
		}
		expiryEN, expiryFA := "Unlimited", "نامحدود"
		if account.ExpiresAt > 0 {
			expiryEN = time.UnixMilli(account.ExpiresAt).UTC().Format("2006-01-02 15:04 UTC")
			expiryFA = telegramPersianDigits.Replace(time.UnixMilli(account.ExpiresAt).UTC().Format("2006-01-02 15:04") + " به وقت جهانی")
		}
		var statusKeyboard any
		if status == "active" {
			statusKeyboard = telegramKeyboard{Rows: [][]telegramButton{{button("Get link", "دریافت لینک", "config"), button("Get QR code", "دریافت کد QR", "qr")}}}
		}
		nextEN, nextFA := "", ""
		switch status {
		case "active":
			nextEN, nextFA = "\nUse the buttons below to get your link or QR code.", "\nبا دکمه‌های پایین لینک یا کد QR را بگیرید."
		case "Preparing":
			nextEN, nextFA = "\nCheck My account again shortly.", "\nکمی بعد دوباره «حساب من» را باز کنید."
		case "disabled", "expired", "exhausted":
			nextEN, nextFA = "\nAsk the service administrator to activate or renew this account.", "\nبرای فعال‌سازی یا تمدید حساب با مسئول سرویس تماس بگیرید."
		}
		send(fmt.Sprintf("Status: %s\nUsed: %.1f MiB\nRemaining: %s\nExpires: %s", status, usage, leftEN, expiryEN)+nextEN,
			telegramPersianDigits.Replace(fmt.Sprintf("وضعیت: %s\nمصرف: %.1f میبی‌بایت\nباقی‌مانده: %s\nانقضا: %s", statusFA, usage, leftFA, expiryFA))+nextFA, statusKeyboard)
		return
	}
	if req.Key != "" && !req.RetryAllowed {
		if req.Cancelled {
			send("You cancelled this request. You can apply again from "+retryEN+".", "درخواست را لغو کرده‌اید. از "+retryFA+" می‌توانید دوباره درخواست بدهید.", nil)
		} else if req.Reviewed {
			send("The administrator declined your request. You can apply again from "+retryEN+".", "مدیر درخواست را رد کرد. از "+retryFA+" می‌توانید دوباره درخواست بدهید.", nil)
		} else {
			send("Your request is waiting for the administrator. Check My account for the result. You can cancel below if you changed your mind.", "درخواست در انتظار بررسی مدیر است. نتیجه در «حساب من» می‌آید. اگر منصرف شده‌اید، می‌توانید درخواست را لغو کنید.",
				telegramKeyboard{Rows: [][]telegramButton{{{Text: tr("Cancel request", "لغو درخواست"), Data: "free:cancel"}}}})
		}
		return
	}
	switch action {
	case "request", "plan":
		if catalog.Paused || catalog.RestoreHold {
			send("The administrator has paused new requests. Open the main menu later to check whether applications are open.", "مدیر پذیرش درخواست تازه را متوقف کرده است. بعداً از منوی اصلی دوباره بررسی کنید.", nil)
			return
		}
		if now.Before(challenge.Expires) {
			send("A verification code is already open. Reply to that message with the code and why you need the configuration.", "یک کد تأیید فعال دارید. به همان پیام با کد و دلیل درخواستتان پاسخ دهید.", nil)
			return
		}
		var plan freePlan
		plans := telegramKeyboard{}
		for _, p := range catalog.Plans {
			if !catalog.offers(p) {
				continue
			}
			data := fmt.Sprintf("free:plan:%s:%d", p.ID, p.Revision)
			label := fmt.Sprintf("%s · %.2f GiB · %d days", p.Input.Name, float64(p.Input.QuotaBytes)/(1<<30), p.Days)
			if language == "fa" {
				label = p.Input.Name + telegramPersianDigits.Replace(fmt.Sprintf(" · %.2f گیبی‌بایت · %d روز", float64(p.Input.QuotaBytes)/(1<<30), p.Days))
			}
			plans.Rows = append(plans.Rows, []telegramButton{{Text: label, Data: data}})
			if action == "request" || value == strings.TrimPrefix(data, "free:plan:") {
				plan = p
			}
		}
		if action == "request" && (len(plans.Rows) > 1 || u.Callback != nil && len(plans.Rows) > 0) {
			send("Choose a free plan.", "یک پلن رایگان انتخاب کنید.", plans)
			return
		}
		if plan.ID == "" {
			if action == "request" {
				send("No free plans are available right now. Check again later for published plans.", "در حال حاضر پلن رایگانی برای درخواست وجود ندارد. بعداً دوباره فهرست پلن‌ها را بررسی کنید.", nil)
			} else {
				send("This plan has changed or been withdrawn. Choose again from the current plans.", "این پلن تغییر کرده یا از عرضه خارج شده است. از فهرست پلن‌های فعلی دوباره انتخاب کنید.",
					telegramKeyboard{Rows: [][]telegramButton{{button("See current plans", "مشاهدهٔ پلن‌های فعلی", "request")}}})
			}
			return
		}
		pending, _, e := store.freeQueue(now)
		if e != nil || pending >= limits.Pending {
			send("The review queue is full. Try again later.", "صف بررسی پر است. بعداً امتحان کنید.", nil)
			return
		}
		if !t.allowFreeChallenge(from.ID, now, limits.CodesHour) {
			send("You have reached the challenge limit. Try again later.", "سقف دریافت کد را پر کرده‌اید. بعداً امتحان کنید.", nil)
			return
		}
		code := token()[:8]
		details := freePlanSummary(plan, tr) + "\n\n"
		prompt := send(details+fmt.Sprintf("Reply to this message with the following code, a space and a short reason (15–240 characters). The code expires in %d minutes.", limits.CodeMinutes)+"\n\n"+code,
			details+telegramPersianDigits.Replace(fmt.Sprintf("در پاسخ به همین پیام، کد زیر را بنویسید و بعد از یک فاصله دلیل درخواستتان را در ۱۵ تا ۲۴۰ نویسه بگویید. کد تا %d دقیقه اعتبار دارد.", limits.CodeMinutes))+"\n\n"+code,
			map[string]bool{"force_reply": true})
		if prompt > 0 {
			t.freeMu.Lock()
			if t.freeChallenges == nil {
				t.freeChallenges = make(map[int64]telegramFreeChallenge)
			}
			t.freeChallenges[from.ID] = telegramFreeChallenge{Code: code, Prompt: prompt, Expires: t.now().Add(time.Duration(limits.CodeMinutes) * time.Minute), Plan: plan}
			t.freeMu.Unlock()
		}
	case "reply":
		t.clearFreeChallenge(from.ID)
		code, reason, _ := strings.Cut(value, " ")
		reason = strings.TrimSpace(reason)
		if code != challenge.Code || !validFreeReason(reason) {
			send("The code or explanation was invalid. Get a fresh code and reply with the code, a space, and a reason of 15–240 characters.", "کد یا دلیل درخواست معتبر نبود. کد تازه بگیرید و در پاسخ، کد را با یک فاصله و دلیل ۱۵ تا ۲۴۰ نویسه‌ای بفرستید.",
				telegramKeyboard{Rows: [][]telegramButton{{button("Get a fresh code", "گرفتن کد تازه", "request")}}})
			return
		}
		live := t.config()
		if !live.FreeAccess || live.Token != c.Token {
			return
		}
		switch store.submitFreePlan(from.ID, token()[:12], reason, challenge.Plan, now) {
		case nil:
			_ = store.updateFreeMember(from.ID, now, func(m *freeMember) {
				m.Started = true
				if m.Language == "" {
					m.Language = language
				}
			})
			send("Your request was registered. The administrator will review it. Follow the result in My account.", "درخواست ثبت شد و مدیر آن را بررسی می‌کند. نتیجه را در «حساب من» پیگیری کنید.",
				telegramKeyboard{Rows: [][]telegramButton{{button("Track request", "پیگیری درخواست", "status")}}})
		case errFreeQueueFull:
			send("The review queue is full. Try again later.", "صف بررسی پر است. بعداً امتحان کنید.", nil)
		case errFreeAdmissionLimit:
			send("The 24-hour application limit is full. Try again later.", "سقف ثبت درخواست در ۲۴ ساعت پر است. بعداً امتحان کنید.", nil)
		case errFreePlanUnavailable, errFreePaused:
			send("The plan changed or requests are paused. Open the plan list again.", "پلن عوض شده یا پذیرش متوقف است. فهرست پلن‌ها را دوباره باز کنید.", nil)
		case ErrConflict:
			send("Your request status changed. Open My account to see the current decision.", "وضعیت درخواست شما تغییر کرده است. آخرین وضعیت را در «حساب من» ببینید.",
				telegramKeyboard{Rows: [][]telegramButton{{button("My account", "حساب من", "status")}}})
		default:
			send("Could not submit. Check /free_status or try again later.", "ثبت درخواست انجام نشد. وضعیت را با /free_status بررسی کنید یا بعداً تلاش کنید.", nil)
		}
	default:
		en, fa := "You do not have an active request or account yet.", "هنوز درخواست باز یا حسابی ندارید."
		if req.RetryAllowed {
			en, fa = "The administrator has allowed a new request. Choose a free plan to apply again.", "مدیر اجازهٔ درخواست دوباره داده است. یک پلن رایگان انتخاب کنید و درخواست تازه بفرستید."
		}
		send(en, fa,
			telegramKeyboard{Rows: [][]telegramButton{{button("Request a free configuration", "درخواست کانفیگ رایگان", "request")}}})
	}
}

func freeRetryDate(at time.Time) (en, fa string) {
	date := at.UTC().Format("2006-01-02 15:04")
	return date + " UTC", telegramPersianDigits.Replace(date) + " به وقت جهانی"
}

func validFreeReason(reason string) bool {
	n := utf8.RuneCountInString(reason)
	return n >= 15 && n <= 240 && !strings.ContainsFunc(reason, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
	})
}

func (t *Telegram) freeQR(ctx context.Context, c TelegramConfig, link string) error {
	png, e := qrcode.Encode(link, qrcode.Medium, 512)
	if e != nil {
		return e
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", strconv.FormatInt(c.ChatID, 10))
	file, e := w.CreateFormFile("photo", "whitedns.png")
	if e != nil {
		return e
	}
	if _, e = file.Write(png); e != nil {
		return e
	}
	if e = w.Close(); e != nil {
		return e
	}
	if e = t.waitAPI(ctx, true, c.ChatID); e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", t.baseURL+"/bot"+c.Token+"/sendPhoto", &body)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return t.request(ctx, req, nil)
}
