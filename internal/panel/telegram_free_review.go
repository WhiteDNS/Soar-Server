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

func (t *Telegram) freeReviewMenu(ctx context.Context, action, value string) error {
	if action == "approve" || action == "reject" {
		key, epoch, _ := strings.Cut(value, "@")
		if epoch != t.freeEpoch {
			return t.showFreeReview(ctx, "queue", "", t.tr("This confirmation expired. Open the request again if it is still pending.", "این تأیید منقضی شده است. اگر درخواست هنوز در انتظار است، دوباره آن را باز کنید."))
		}
		return t.executeFreeReview(ctx, action == "approve", []string{key})
	}
	return t.showFreeReview(ctx, action, value, "")
}

func (t *Telegram) showFreeReview(ctx context.Context, action, value, result string) error {
	list, e := t.h.store.freePending(t.now())
	if e != nil {
		return e
	}
	if action == "requestdetail" {
		for _, req := range list {
			if req.Key != value {
				continue
			}
			chat, _, _ := strings.Cut(req.Key, ":")
			key := req.Key + "@" + t.freeEpoch
			k := telegramKeyboard{Rows: [][]telegramButton{
				{t.button("Approve", "تأیید", "free:approve:"+key), t.button("Reject", "رد", "free:reject:"+key)},
				{t.button("Manage this user", "مدیریت این کاربر", "free:member:"+chat), t.button("Back to requests", "بازگشت به درخواست‌ها", "free:queue")},
			}}
			at := req.At.UTC().Format("2006-01-02 15:04") + t.tr(" UTC", " به وقت جهانی")
			if t.language() == "fa" {
				at = telegramPersianDigits.Replace(at)
			}
			_, e = t.notify(ctx, t.config(), t.tr("Telegram ID", "شناسهٔ تلگرام")+" "+req.displayUser()+" · "+at+"\n"+freePlanSummary(req.plan(), t.tr)+"\n\n"+
				t.tr("User explanation", "توضیح کاربر")+"\n"+req.Reason+"\n\n"+
				t.tr("Approval creates an account using these terms. Rejection starts the configured waiting period. Manage this user can allow an earlier request.",
					"تأیید، حسابی با همین شرایط می‌سازد. با رد درخواست، مهلت تنظیم‌شده شروع می‌شود. از «مدیریت این کاربر» می‌توانید درخواست زودتر را مجاز کنید."), k)
			return e
		}
		result = t.tr("This request expired or was already reviewed. The list is up to date.", "این درخواست منقضی شده یا قبلاً بررسی شده است. فهرست به‌روز است.")
	}
	k := telegramKeyboard{}
	var selected, preview []string
	for _, req := range list {
		chosen := (slices.Contains(t.freeSelected, req.Key) || action == "selectall") && action != "unselect"
		if action == "select" && value == req.Key {
			chosen = !chosen
		}
		mark := "☐"
		if chosen {
			mark = "☑"
			selected = append(selected, req.Key)
			preview = append(preview, req.displayUser()+" · "+req.plan().Input.Name)
		}
		k.Rows = append(k.Rows, []telegramButton{
			{Text: req.displayUser() + " · " + req.plan().Input.Name, Data: "free:requestdetail:" + req.Key},
			{Text: mark, Data: "free:select:" + req.Key},
		})
	}
	t.freeSelected = selected
	if action == "bulkapprove" || action == "bulkreject" {
		if len(preview) == 0 {
			return errors.New("select at least one pending request")
		}
		label := t.tr("Approve selected requests", "تأیید درخواست‌های انتخاب‌شده")
		if action == "bulkreject" {
			label = t.tr("Reject selected requests", "رد درخواست‌های انتخاب‌شده")
		}
		return t.confirm(ctx, telegramConfirmation{Action: "free-" + action, FreeIDs: slices.Clone(selected)}, label+"\n"+strings.Join(preview, "\n"))
	}
	if len(list) > 0 {
		k.Rows = append(k.Rows, []telegramButton{t.button("Select all", "انتخاب همه", "free:selectall"), t.button("Clear", "پاک‌کردن انتخاب", "free:unselect")},
			[]telegramButton{t.button("Approve selected", "تأیید گروهی", "free:bulkapprove"), t.button("Reject selected", "رد گروهی", "free:bulkreject")})
	}
	k.Rows = append(k.Rows, []telegramButton{t.button("Back to dashboard", "بازگشت به داشبورد", "free:manage")})
	text := t.tr("No pending requests.", "درخواستی در انتظار بررسی نیست.")
	if len(list) != 0 {
		text = fmt.Sprintf(t.tr("Pending requests: %d · Selected: %d", "درخواست‌های در انتظار: %d · انتخاب‌شده: %d"), len(list), len(selected))
		if t.language() == "fa" {
			text = telegramPersianDigits.Replace(text)
		}
		text += "\n" + t.tr("Open a request to review it, or use the checkboxes for group review.", "برای بررسی، درخواست را باز کنید. برای بررسی گروهی، گزینه‌های کنار درخواست‌ها را علامت بزنید.")
	}
	if result != "" {
		text = result + "\n\n" + text
	}
	_, e = t.notify(ctx, t.config(), text, k)
	return e
}

func (t *Telegram) executeFreeReview(ctx context.Context, approve bool, keys []string) error {
	c := t.config()
	if approve && !c.FreeAccess {
		return errFreePaused
	}
	now := t.now()
	var outcomes []string
	recipients := make(map[string][]string)
	issued := false
	checks := t.freeReviewProofs(ctx, approve, keys)
	for i, key := range keys {
		var e error
		if approve {
			e = checks[i].err
			if e == nil {
				_, e = t.h.store.issueFree(key, t.now(), false, checks[i].proof)
			}
			issued = issued || e == nil
		} else {
			e = t.h.store.db.Update(func(tx *bolt.Tx) error { return reviewFree(tx, key, "", now, false) })
		}
		id, _, _ := strings.Cut(key, ":")
		label := t.tr("Approved. The user can get their config from My account when it is ready.", "تأیید شد. پس از آماده‌شدن، کانفیگ از «حساب من» قابل دریافت است.")
		if !approve {
			label = t.tr("Declined. The user can apply again once the waiting period ends.", "رد شد. کاربر پس از پایان مهلت می‌تواند دوباره درخواست بدهد.")
		}
		switch {
		case e == nil:
			recipient, _ := strconv.ParseInt(id, 10, 64)
			t.queueFreeNotice(c, recipient)
		case errors.Is(e, ErrNotFound):
			label = t.tr("Expired or already reviewed", "منقضی یا بررسی‌شده")
		case errors.Is(e, errFreeJoinMissing):
			label = t.tr("Still pending. The user must join the required chats.", "در انتظار ماند. کاربر باید عضو کانال‌ها و گروه‌های الزامی باشد.")
		case errors.Is(e, errFreeJoinUnavailable):
			label = t.tr("Still pending. Telegram could not verify membership. Check bot access or try again.", "در انتظار ماند. بررسی عضویت ممکن نشد. دسترسی ربات را بررسی کنید یا دوباره تلاش کنید.")
		case errors.Is(e, errFreeDailyLimit):
			label = t.tr("Still pending. The 24-hour issuance limit is full.", "در انتظار ماند. سقف صدور در ۲۴ ساعت پر است.")
		case errors.Is(e, errFreePanelSlots):
			label = t.tr("Still pending. The remaining panel slots are reserved.", "در انتظار ماند. جاهای باقیماندهٔ پنل محفوظ‌اند.")
		case errors.Is(e, errFreeAccountSlots):
			label = t.tr("Still pending. The free account limit is full.", "در انتظار ماند. سقف تعداد حساب‌های رایگان پر است.")
		case errors.Is(e, errFreeStreamSlots):
			label = t.tr("Still pending. The plan exceeds the available stream budget.", "در انتظار ماند. بودجهٔ اتصال باقی‌مانده برای این پلن کافی نیست.")
		case errors.Is(e, errFreePlanUnavailable), errors.Is(e, errFreePaused):
			label = t.tr("Still pending. Check the plan, public access and the user's block status.", "در انتظار ماند. پلن، دسترسی عمومی و وضعیت مسدودی کاربر را بررسی کنید.")
		default:
			label = t.tr("Failed to save · check the queue", "ذخیره نشد · صف را بررسی کنید")
		}
		if _, exists := recipients[label]; !exists {
			outcomes = append(outcomes, label)
		}
		recipients[label] = append(recipients[label], id)
	}
	lines := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		lines = append(lines, strings.Join(recipients[outcome], ", ")+" · "+outcome)
	}
	t.freeSelected = nil
	err := t.showFreeReview(ctx, "queue", "", strings.Join(lines, "\n"))
	if issued {
		if e := t.h.manager.Reconcile(); e != nil {
			t.h.manager.logEvent("error", "panel", "", "reconcile_failed", e, 0)
			_, _ = t.notify(ctx, c, t.tr("Accounts saved. Runtime preparation will be retried.", "حساب‌ها ذخیره شدند. آماده‌سازی سرویس دوباره تلاش می‌شود."), nil)
		}
	}
	return err
}
