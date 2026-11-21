package panel

import (
	"context"
	"errors"
	"strconv"
)

func (t *Telegram) freeMemberMenu(ctx context.Context, id int64) error {
	s := t.h.store
	m, e := s.freeMember(id)
	if e != nil {
		return e
	}
	req, e := s.freeRequest(id, t.now())
	if e != nil {
		return e
	}
	value := strconv.FormatInt(id, 10)
	text := t.tr("Telegram user", "کاربر تلگرام") + " · " + value
	access := t.button("Block Telegram access", "مسدودکردن دسترسی ربات", "free:block:"+value)
	if m.Blocked {
		text += t.tr("\nAccess is blocked. Unblocking restores bot access.", "\nدسترسی ربات مسدود است. با رفع مسدودی، کاربر دوباره می‌تواند از ربات استفاده کند.")
		access = t.button("Unblock Telegram access", "رفع مسدودی دسترسی ربات", "free:unblock:"+value)
	} else {
		text += t.tr("\nTelegram access is allowed.", "\nدسترسی این شناسه به ربات باز است.")
	}
	k := telegramKeyboard{Rows: [][]telegramButton{{access}}}
	var account Client
	if req.AccountID != "" {
		account, e = s.Get(req.AccountID)
		if e != nil && !errors.Is(e, ErrNotFound) {
			return e
		}
	}
	switch {
	case account.ID != "":
		text += t.tr("\nLinked account status: ", "\nوضعیت حساب متصل: ") + t.accountStatus(account)
		text += t.tr("\nUse the renewal form to change limits and expiry.", "\nبرای تغییر تاریخ انقضا و محدودیت‌ها، فرم تمدید را باز کنید.")
	case req.Key == "":
		text += t.tr("\nNo current request or linked account.", "\nدرخواست جاری یا حساب متصل وجود ندارد.")
	case !req.Reviewed:
		text += t.tr("\nA request is waiting for review. Open it to see the plan and reason.", "\nدرخواست در انتظار بررسی است. برای دیدن پلن و دلیل کاربر، درخواست را باز کنید.")
		k.Rows = append(k.Rows, []telegramButton{t.button("Open request", "بازکردن درخواست", "free:requestdetail:"+req.Key)})
	default:
		switch {
		case req.AccountID != "":
			text += t.tr("\nThe previous configuration is missing from the panel.", "\nکانفیگ قبلی دیگر در پنل موجود نیست.")
		case req.Cancelled:
			text += t.tr("\nThe user cancelled the last request.", "\nکاربر درخواست قبلی را لغو کرده است.")
		default:
			text += t.tr("\nThe last request was declined.", "\nدرخواست قبلی رد شده است.")
		}
		catalog, err := t.h.store.freeCatalog()
		if err != nil {
			return err
		}
		en, fa := freeRetryDate(req.retryAt(catalog.limits()))
		text += "\n" + t.tr("Original retry time: "+en, "پایان مهلت اولیهٔ درخواست مجدد: "+fa)
		if req.RetryAllowed {
			text += t.tr("\nOne early request is allowed. Verification and review still apply.", "\nاجازهٔ یک درخواست زودتر داده شده است. بررسی و تأیید همچنان لازم است.")
			k.Rows = append(k.Rows, []telegramButton{t.button("Withdraw early retry", "لغو اجازهٔ درخواست مجدد", "free:retryoff:"+value)})
		} else {
			k.Rows = append(k.Rows, []telegramButton{t.button("Allow one early retry", "اجازهٔ یک درخواست مجدد", "free:retry:"+value)})
		}
	}
	if m.Started && !m.Blocked && (req.AccountID == "" || account.ID != "" || req.RetryAllowed) {
		k.Rows = append(k.Rows, []telegramButton{t.button("Issue or renew a configuration", "ساخت یا تمدید کانفیگ", "free:customfor:"+value)})
	} else if !m.Started {
		text += t.tr("\nThe recipient must open the bot before you can issue a configuration.", "\nبرای صدور کانفیگ، گیرنده ابتدا باید ربات را باز کند.")
	}
	k.Rows = append(k.Rows, []telegramButton{t.button("Back to accounts", "بازگشت به حساب‌ها", "free:accounts")})
	_, e = t.notify(ctx, t.config(), text, k)
	return e
}
