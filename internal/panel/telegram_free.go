package panel

import (
	"bytes"
	"context"
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
	owned := freeReadAction(action) && t.h.store.hasFreeOwner(from.ID)
	if !t.allowFreeJob(from.ID, now, action, owned) {
		return
	}
	ctx = withFreeLane(ctx, freeAdmissionLane)
	if owned {
		ctx = withFreeLane(ctx, freeOwnerLane)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c.ChatID = from.ID
	store := t.h.store
	member, err := store.freeMember(from.ID)
	if err != nil || member.Blocked {
		return
	}
	catalog, err := store.freeCatalog()
	if err != nil {
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
	send := func(en, fa string, markup any) int64 {
		// A blocked customer chat must not mark the management bot disconnected.
		live := t.config()
		if !live.Enabled || !live.FreeAccess || live.Token != c.Token {
			return 0
		}
		id, _ := t.sendText(ctx, c, tr(en, fa), markup)
		return id
	}
	if u.Callback != nil {
		defer func() {
			_ = t.call(ctx, c, "answerCallbackQuery", map[string]string{"callback_query_id": u.Callback.ID}, nil)
		}()
	}
	if catalog.RestoreHold {
		send("Public service is paused while the administrator reviews the restored backup.",
			"سرویس عمومی تا پایان بررسی بکاپ بازیابی‌شده متوقف است.", nil)
		return
	}
	if action == "menu" {
		_ = store.updateFreeMember(from.ID, now, func(m *freeMember) {
			m.Started = true
			if m.Language == "" {
				m.Language = language
			}
		})
		welcome := catalog.Text["welcome:"+language]
		if welcome == "" {
			welcome = tr("WhiteDNS free configurations. An administrator reviews new requests.", "کانفیگ رایگان WhiteDNS. مدیر درخواست‌های جدید را بررسی می‌کند.")
		}
		send(welcome, welcome, telegramKeyboard{Rows: [][]telegramButton{
			{{Text: tr("Free plans", "پلن‌های رایگان"), Data: "free:request"}},
			{{Text: tr("My status", "وضعیت من"), Data: "free:status"}},
			{{Text: tr("My config", "کانفیگ من"), Data: "free:config"}, {Text: tr("QR code", "کد QR"), Data: "free:qr"}},
			{{Text: tr("Connection guide", "راهنمای اتصال"), Data: "free:guide"}, {Text: tr("Terms", "شرایط"), Data: "free:terms"}},
			{{Text: tr("Language", "زبان"), Data: "free:language"}},
		}})
		return
	}
	switch action {
	case "language":
		send("Choose a language or change your broadcast preferences.", "زبان یا تنظیم دریافت پیام همگانی را انتخاب کنید.", telegramKeyboard{Rows: [][]telegramButton{
			{{Text: "فارسی", Data: "free:lang:fa"}, {Text: "English", Data: "free:lang:en"}},
			{{Text: tr("Announcements on", "اعلان همگانی روشن"), Data: "free:opt:on"}, {Text: tr("Announcements off", "اعلان همگانی خاموش"), Data: "free:opt:off"}},
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
				send("Preference saved. Send /start for the menu.", "ترجیح ذخیره شد. برای منو /start را بفرستید.", nil)
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
				body = tr("Open My config or QR code, select an available engine and import the profile into WhiteDNS.", "در «کانفیگ من» یا «کد QR» موتور را انتخاب کنید و پروفایل را در WhiteDNS وارد کنید.")
			}
		}
		send(body, body, nil)
		return
	case "cancel":
		t.clearFreeChallenge(from.ID)
		if cancelled, e := store.cancelFree(from.ID, now); e == nil && cancelled {
			send("Your pending request was cancelled.", "درخواست در انتظار شما لغو شد.", nil)
		} else if e == nil {
			send("There is no pending request to cancel.", "درخواستی برای لغو وجود ندارد.", nil)
		}
		return
	}
	req, err := store.freeRequest(c.ChatID, now)
	if err != nil {
		send("Service unavailable. Try again later.", "سرویس موقتاً در دسترس نیست. بعداً امتحان کنید.", nil)
		return
	}
	if req.AccountID != "" {
		account, err := store.Get(req.AccountID)
		if err != nil {
			send("This account is unavailable.", "این حساب در دسترس نیست.", nil)
			return
		}
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
				send(link, link, nil)
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
			leftFA = leftEN
		}
		expiryEN, expiryFA := "Unlimited", "نامحدود"
		if account.ExpiresAt > 0 {
			expiryEN = time.UnixMilli(account.ExpiresAt).UTC().Format("2006-01-02 15:04 UTC")
			expiryFA = expiryEN
		}
		send(fmt.Sprintf("Status: %s\nUsed: %.1f MiB\nRemaining: %s\nExpires: %s", status, usage, leftEN, expiryEN),
			fmt.Sprintf("وضعیت: %s\nمصرف: %.1f MiB\nباقی‌مانده: %s\nانقضا: %s", statusFA, usage, leftFA, expiryFA), nil)
		return
	}
	if req.Key != "" {
		if req.Cancelled {
			send("You cancelled this request. You can apply again after 24 hours.", "درخواست لغو شد. پس از ۲۴ ساعت می‌توانید دوباره درخواست بدهید.", nil)
		} else if req.Reviewed {
			send("Request declined. Try again 24 hours after review.", "درخواست رد شد. ۲۴ ساعت پس از بررسی می‌توانید دوباره درخواست بدهید.", nil)
		} else {
			send("Your request is waiting for review.", "درخواست شما در انتظار بررسی است.",
				telegramKeyboard{Rows: [][]telegramButton{{{Text: tr("Cancel request", "لغو درخواست"), Data: "free:cancel"}}}})
		}
		return
	}
	switch action {
	case "request", "plan":
		if catalog.Paused || catalog.RestoreHold {
			send("New requests are paused.", "پذیرش درخواست جدید متوقف است.", nil)
			return
		}
		if now.Before(challenge.Expires) {
			send("Reply to the challenge already sent to you.", "به پیام چالش قبلی پاسخ دهید.", nil)
			return
		}
		var plan freePlan
		plans := telegramKeyboard{}
		for _, p := range catalog.Plans {
			if !catalog.offers(p) {
				continue
			}
			data := fmt.Sprintf("free:plan:%s:%d", p.ID, p.Revision)
			plans.Rows = append(plans.Rows, []telegramButton{{Text: fmt.Sprintf("%s · %.2f GiB · %d %s", p.Input.Name, float64(p.Input.QuotaBytes)/(1<<30), p.Days, tr("days", "روز")), Data: data}})
			if action == "request" || value == strings.TrimPrefix(data, "free:plan:") {
				plan = p
			}
		}
		if action == "request" && len(plans.Rows) > 1 {
			send("Choose a free plan.", "یک پلن رایگان انتخاب کنید.", plans)
			return
		}
		if plan.ID == "" {
			send("This plan is unavailable. Open the plan list again.", "این پلن در دسترس نیست. فهرست پلن‌ها را دوباره باز کنید.", nil)
			return
		}
		pending, _, e := store.freeQueue(now)
		if e != nil || pending >= freeQueueLimit {
			send("The review queue is full. Try again later.", "صف بررسی پر است. بعداً امتحان کنید.", nil)
			return
		}
		if !t.allowFreeChallenge(from.ID, now) {
			send("You have reached the challenge limit. Try again later.", "سقف دریافت کد را پر کرده‌اید. بعداً امتحان کنید.", nil)
			return
		}
		code := token()[:8]
		details := freePlanSummary(plan, tr) + "\n\n"
		prompt := send(details+"Reply to this message with the following code, a space and a short reason (15–240 characters). The code expires in five minutes.\n\n"+code,
			details+"در پاسخ به همین پیام، کد زیر را بنویسید و بعد از یک فاصله دلیل درخواستتان را در ۱۵ تا ۲۴۰ نویسه بگویید. کد تا پنج دقیقه اعتبار دارد.\n\n"+code,
			map[string]bool{"force_reply": true})
		if prompt > 0 {
			t.freeMu.Lock()
			if t.freeChallenges == nil {
				t.freeChallenges = make(map[int64]telegramFreeChallenge)
			}
			t.freeChallenges[from.ID] = telegramFreeChallenge{Code: code, Prompt: prompt, Expires: now.Add(freeChallengeLifetime), Plan: plan}
			t.freeMu.Unlock()
		}
	case "reply":
		t.clearFreeChallenge(from.ID)
		code, reason, _ := strings.Cut(value, " ")
		reason = strings.TrimSpace(reason)
		if code != challenge.Code || !validFreeReason(reason) {
			send("Invalid reply. Use /free to request a fresh code.", "پاسخ معتبر نیست. برای کد تازه /free را بفرستید.", nil)
			return
		}
		live := t.config()
		if !live.FreeAccess || live.Token != c.Token {
			return
		}
		switch store.submitFreePlan(from.ID, token()[:12], reason, challenge.Plan, now) {
		case nil:
			send("Your request was sent for manual review.", "درخواست شما برای بررسی مدیر ثبت شد.", nil)
		case errFreeQueueFull:
			send("The review queue is full. Try again later.", "صف بررسی پر است. بعداً امتحان کنید.", nil)
		case errFreePlanUnavailable, errFreePaused:
			send("The plan changed or requests are paused. Open the plan list again.", "پلن عوض شده یا پذیرش متوقف است. فهرست پلن‌ها را دوباره باز کنید.", nil)
		default:
			send("Could not submit. Check /free_status or try again later.", "ثبت درخواست انجام نشد. وضعیت را با /free_status بررسی کنید یا بعداً تلاش کنید.", nil)
		}
	default:
		send("Use /free to request access.", "برای درخواست کانفیگ رایگان /free را بفرستید.", nil)
	}
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
	if e = t.waitAPI(ctx, true); e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", t.baseURL+"/bot"+c.Token+"/sendPhoto", &body)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return t.request(ctx, req, nil)
}
