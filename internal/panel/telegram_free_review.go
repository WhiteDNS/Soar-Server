package panel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

func (t *Telegram) freeReviewMenu(ctx context.Context, action, value string) error {
	if action == "approve" || action == "reject" {
		key, epoch, _ := strings.Cut(value, "@")
		if epoch != t.freeEpoch {
			return errors.New("confirmation expired; reopen the request")
		}
		return t.executeFreeReview(ctx, action == "approve", []string{key})
	}
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
				{t.button("Block requester", "مسدودکردن کاربر", "free:block:"+chat), t.button("Queue", "صف", "free:queue")},
			}}
			_, e = t.notify(ctx, t.config(), chat+" · "+req.At.UTC().Format(time.RFC3339)+"\n"+freePlanSummary(req.plan(), t.tr)+"\n\n"+
				t.tr("User explanation", "توضیح کاربر")+"\n"+req.Reason, k)
			return e
		}
		return ErrNotFound
	}
	k := telegramKeyboard{}
	var selected, preview []string
	for _, req := range list {
		chosen := (slices.Contains(t.freeSelected, req.Key) || action == "selectall") && action != "unselect"
		if action == "select" && value == req.Key {
			chosen = !chosen
		}
		mark := "☐"
		chat, _, _ := strings.Cut(req.Key, ":")
		if chosen {
			mark = "☑"
			selected = append(selected, req.Key)
			preview = append(preview, chat+" · "+req.plan().Input.Name)
		}
		k.Rows = append(k.Rows, []telegramButton{
			{Text: chat + " · " + req.plan().Input.Name, Data: "free:requestdetail:" + req.Key},
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
	k.Rows = append(k.Rows, []telegramButton{t.button("Free configurations", "کانفیگ رایگان", "free:manage")})
	_, e = t.notify(ctx, t.config(), fmt.Sprintf(t.tr("Pending requests: %d · Selected: %d", "درخواست‌های باز: %d · انتخاب‌شده: %d"), len(list), len(selected)), k)
	return e
}

func (t *Telegram) executeFreeReview(ctx context.Context, approve bool, keys []string) error {
	c := t.config()
	if approve && !c.FreeAccess {
		return errFreePaused
	}
	now := t.now()
	lines := []string{}
	issued := false
	for _, key := range keys {
		var e error
		if approve {
			_, e = t.h.store.approveFree(key, now)
			issued = issued || e == nil
		} else {
			e = t.h.store.db.Update(func(tx *bolt.Tx) error { return reviewFree(tx, key, "", now) })
		}
		id, _, _ := strings.Cut(key, ":")
		label := t.tr("Approved", "تأیید شد")
		if !approve {
			label = t.tr("Rejected", "رد شد")
		}
		switch {
		case e == nil:
			recipient, _ := strconv.ParseInt(id, 10, 64)
			t.queueFreeNotice(c, recipient)
		case errors.Is(e, ErrNotFound):
			label = t.tr("Expired or already reviewed", "منقضی یا بررسی‌شده")
		case errors.Is(e, errFreeDailyLimit), errors.Is(e, errFreeCapacity):
			label = t.tr("Still pending · issuance capacity reached", "در انتظار · سقف صدور پر است")
		case errors.Is(e, errFreePlanUnavailable), errors.Is(e, errFreePaused):
			label = t.tr("Still pending · plan or access unavailable", "در انتظار · پلن یا دسترسی متوقف است")
		default:
			label = t.tr("Failed to save · check the queue", "ذخیره نشد · صف را بررسی کنید")
		}
		lines = append(lines, id+" · "+label)
	}
	if issued {
		if e := t.h.manager.Reconcile(); e != nil {
			lines = append(lines, t.tr("Accounts saved. Runtime preparation will be retried.", "حساب‌ها ذخیره شدند. آماده‌سازی سرویس دوباره تلاش می‌شود."))
		}
	}
	t.freeSelected = nil
	_, e := t.notify(ctx, c, strings.Join(lines, "\n"), telegramKeyboard{Rows: [][]telegramButton{{t.button("Open queue", "بازکردن صف", "free:queue")}}})
	return e
}
