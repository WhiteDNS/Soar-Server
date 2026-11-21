package panel

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

const (
	maxFreePending        = 40
	maxFreeAdmissions     = 1000
	maxFreeIssuance       = MaxClients
	maxFreeMenuQueue      = 1200
	maxFreeActionQueue    = 96
	firstFreeTrafficField = 8
)

type freeLimits struct {
	Issuance, Admissions, Pending, Accounts, Headroom, Streams int
	RetryHours, PendingHours, ApplicationsMinute, OwnerMinute  int
	CodeMinutes, CodesHour, ActionSeconds, MenuDelayMS         int
	MenuQueue, ApplicationQueue, OwnerQueue                    int
}

func (c freeCatalog) limits() freeLimits {
	if c.Limits != nil {
		return *c.Limits
	}
	return freeLimits{
		Issuance: freeDailyLimit, Admissions: freeAdmissionLimit, Pending: freeQueueLimit,
		Accounts: freeOwnedLimit, Headroom: freePanelHeadroom, Streams: freeStreamBudget,
		RetryHours: 24, PendingHours: 24, ApplicationsMinute: freeTrafficLimit - 4, OwnerMinute: 30,
		CodeMinutes: int(freeChallengeLifetime / time.Minute), CodesHour: 3, ActionSeconds: 3, MenuDelayMS: 750,
		MenuQueue: freeMenuCapacity, ApplicationQueue: 24, OwnerQueue: 24,
	}
}

type freeLimitField struct {
	key, en, fa string
	min, max    int
	value       func(*freeLimits) *int
}

var freeLimitFields = []freeLimitField{
	{"issue", "New configs / 24 hours", "صدور جدید در ۲۴ ساعت", 1, maxFreeIssuance, func(p *freeLimits) *int { return &p.Issuance }},
	{"admit", "Accepted applications / 24 hours", "ثبت درخواست در ۲۴ ساعت", 1, maxFreeAdmissions, func(p *freeLimits) *int { return &p.Admissions }},
	{"pending", "Requests waiting for review", "درخواست‌های در انتظار بررسی", 1, maxFreePending, func(p *freeLimits) *int { return &p.Pending }},
	{"owned", "Existing free accounts", "تعداد حساب‌های رایگان موجود", 1, MaxClients, func(p *freeLimits) *int { return &p.Accounts }},
	{"reserved", "Panel account slots reserved", "جای محفوظ برای حساب‌های پنل", 0, MaxClients - 1, func(p *freeLimits) *int { return &p.Headroom }},
	{"streams", "Combined active free streams", "مجموع سقف اتصال حساب‌های رایگان فعال", 1, MaxClients * 128, func(p *freeLimits) *int { return &p.Streams }},
	{"retry", "Wait before reapplying (hours)", "مهلت درخواست دوباره (ساعت)", 1, 168, func(p *freeLimits) *int { return &p.RetryHours }},
	{"age", "Pending request expires after (hours)", "انقضای درخواست بررسی‌نشده (ساعت)", 1, 168, func(p *freeLimits) *int { return &p.PendingHours }},
	{"intake", "Application actions / minute", "عملیات ثبت درخواست در دقیقه", 1, 60, func(p *freeLimits) *int { return &p.ApplicationsMinute }},
	{"owner", "Existing-account actions / minute", "عملیات کاربران دارای حساب در دقیقه", 1, 120, func(p *freeLimits) *int { return &p.OwnerMinute }},
	{"code", "Verification code validity (minutes)", "اعتبار کد تأیید (دقیقه)", 1, 15, func(p *freeLimits) *int { return &p.CodeMinutes }},
	{"attempt", "New codes / user / hour", "کد تازه برای هر کاربر در ساعت", 1, 15, func(p *freeLimits) *int { return &p.CodesHour }},
	{"gap", "Application action gap (seconds)", "فاصلهٔ عملیات ثبت (ثانیه)", 1, 30, func(p *freeLimits) *int { return &p.ActionSeconds }},
	{"repeat", "Duplicate menu cooldown (milliseconds)", "مهلت تکرار منو (میلی‌ثانیه)", 250, 5000, func(p *freeLimits) *int { return &p.MenuDelayMS }},
	{"menuq", "Public menu queue size", "ظرفیت صف منوی عمومی", 50, maxFreeMenuQueue, func(p *freeLimits) *int { return &p.MenuQueue }},
	{"appq", "Application action queue size", "ظرفیت صف عملیات ثبت", 4, maxFreeActionQueue, func(p *freeLimits) *int { return &p.ApplicationQueue }},
	{"acctq", "Existing-account action queue size", "ظرفیت صف عملیات حساب‌های موجود", 4, maxFreeActionQueue, func(p *freeLimits) *int { return &p.OwnerQueue }},
}

func (t *Telegram) freeLimitsMenu(ctx context.Context, value, status string) error {
	if telegramActor(ctx).Role == telegramFreeAdmin {
		return ErrNotFound
	}
	catalog, err := t.h.store.freeCatalog()
	if err != nil {
		return err
	}
	policy := catalog.limits()
	k := telegramKeyboard{}
	for i, f := range freeLimitFields {
		if value == f.key {
			hint := fmt.Sprintf(t.tr("Reply with a whole number from %d to %d. Current value: %d.", "عددی صحیح از %d تا %d در پاسخ بفرستید. مقدار فعلی %d است."), f.min, f.max, *f.value(&policy))
			if t.language() == "fa" {
				hint = telegramPersianDigits.Replace(hint)
			}
			return t.freePrompt(ctx, "limit:"+value, catalog.Revision, t.tr(f.en, f.fa)+"\n"+hint)
		}
		if (value == "traffic") != (i >= firstFreeTrafficField) {
			continue
		}
		en := fmt.Sprintf("%s · %d", f.en, *f.value(&policy))
		fa := telegramPersianDigits.Replace(fmt.Sprintf("%s · %d", f.fa, *f.value(&policy)))
		k.Rows = append(k.Rows, []telegramButton{t.button(en, fa, "free:limits:"+f.key)})
	}
	if value != "" && value != "traffic" {
		return ErrNotFound
	}
	t.conversation = nil
	text := t.tr("Free service limits\nSelect a limit to edit it. Existing accounts keep their plan settings.", "محدودیت‌های سرویس رایگان\nبرای تغییر، روی محدودیت بزنید. تنظیمات پلن حساب‌های موجود حفظ می‌شود.")
	if value == "traffic" {
		text = t.tr("Telegram traffic and verification\nSelect a setting to edit it.", "ترافیک تلگرام و تأیید\nبرای تغییر، روی تنظیم بزنید.")
		k.Rows = append(k.Rows, []telegramButton{t.button("Back to capacity limits", "بازگشت به سقف‌های ظرفیت", "free:limits")})
	} else {
		k.Rows = append(k.Rows, []telegramButton{t.button("Traffic and verification", "ترافیک و تأیید", "free:limits:traffic")},
			[]telegramButton{t.button("Back to dashboard", "بازگشت به داشبورد", "free:manage")})
	}
	_, err = t.notify(ctx, t.config(), status+text, k)
	return err
}

func (t *Telegram) saveFreeLimit(ctx context.Context, d *telegramConversation, key, text string) error {
	if telegramActor(ctx).Role == telegramFreeAdmin {
		return ErrNotFound
	}
	for i, f := range freeLimitFields {
		if key != f.key {
			continue
		}
		n, err := strconv.Atoi(telegramDigits(text))
		if err != nil || n < f.min || n > f.max {
			_, err = t.notify(ctx, t.config(), t.tr("That value is outside the allowed range. Reply to the original prompt with a valid number.", "این مقدار خارج از بازهٔ مجاز است. یک عدد معتبر در پاسخ به پیام قبلی بفرستید."), nil)
			return err
		}
		if err = t.h.store.changeFreeCatalog(d.FreeRevision, func(c *freeCatalog) error {
			p := c.limits()
			*f.value(&p) = n
			c.Limits = &p
			return nil
		}); err != nil {
			return err
		}
		_ = t.h.store.Audit("telegram.free_limit_changed", fmt.Sprintf("%s=%d", key, n))
		page := ""
		if i >= firstFreeTrafficField {
			page = "traffic"
		}
		return t.freeLimitsMenu(ctx, page, t.tr("Limit saved.\n\n", "محدودیت ذخیره شد.\n\n"))
	}
	return ErrNotFound
}
