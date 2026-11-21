package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	bolt "go.etcd.io/bbolt"
)

func (t *Telegram) freeStatsMenu(ctx context.Context) error {
	now := t.now()
	s := t.h.store
	audience, e := s.freeAudience("stats", now)
	if e != nil {
		return e
	}
	accounts, e := s.List()
	if e != nil {
		return e
	}
	active, connected, total := 0, 0, 0
	counts := t.h.manager.ActiveCounts()
	for _, account := range accounts {
		if account.InstallerProbe {
			continue
		}
		total++
		if account.Status(now) == "active" {
			active++
		}
		if counts[account.ID] > 0 {
			connected++
		}
	}
	pending, denied, approved, issued, admitted, streams := 0, 0, 0, 0, 0, 0
	var limits freeLimits
	byEngine, byPlan := map[string]int{}, map[string]int{}
	e = s.db.View(func(tx *bolt.Tx) error {
		catalog, err := readFreeCatalog(tx)
		if err != nil {
			return err
		}
		limits = catalog.limits()
		b := tx.Bucket(meta).Bucket(freeRequests)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var r telegramFreeRequest
			if e := json.Unmarshal(raw, &r); e != nil {
				return e
			}
			issued += recentFreeEvents(r.PriorIssuances, now)
			admitted += recentFreeEvents(r.PriorAdmissions, now)
			if now.Before(r.At.Add(freeCooldown)) {
				admitted++
			}
			switch {
			case r.AccountID != "":
				if now.Before(r.decisionTime().Add(freeCooldown)) {
					issued++
				}
				account, e := getClient(tx, r.AccountID)
				if e != nil && !errors.Is(e, ErrNotFound) {
					return e
				}
				if e == nil {
					approved++
					if account.Status(now) == "active" {
						streams += freeAccountStreams(account)
					}
					if r.Linked && r.Plan == nil {
						byPlan[t.tr("Linked account", "حساب متصل‌شده")]++
					} else {
						byPlan[r.plan().Input.Name]++
					}
					for _, engine := range engineSelections[account.Engines] {
						byEngine[engine]++
					}
				}
			case r.Reviewed && !r.Cancelled && now.Before(r.decisionTime().Add(freeCooldown)):
				denied++
			case !r.Reviewed && !expiredFreeRequest(tx, r, now, limits):
				pending++
			}
			return nil
		})
	})
	if e != nil {
		return e
	}
	text := fmt.Sprintf(t.tr("Telegram users %d\nPanel accounts %d · Active %d · With open streams %d\nFree accounts %d · Pending %d · Rejected in last 24h %d\nIssuance remaining %d/%d",
		"کاربران تلگرام %d\nحساب‌های پنل %d · فعال %d · دارای جریان باز %d\nحساب رایگان %d · در انتظار %d · ردشده در ۲۴ ساعت %d\nظرفیت صدور باقی‌مانده %d/%d"),
		len(audience), total, active, connected, approved, pending, denied, max(0, limits.Issuance-issued), limits.Issuance)
	text += fmt.Sprintf(t.tr("\nApplications (24h) %d/%d · Queue %d/%d\nFree account slots %d/%d · Active stream budget %d/%d",
		"\nدرخواست در ۲۴ ساعت %d/%d · صف %d/%d\nحساب رایگان %d/%d · بودجهٔ اتصال فعال %d/%d"),
		admitted, limits.Admissions, pending, limits.Pending, approved, limits.Accounts, streams, limits.Streams)
	text += fmt.Sprintf(t.tr("\nPanel slots used %d/%d · Reserved %d", "\nجای اشغال‌شده در پنل %d/%d · محفوظ %d"), len(accounts), MaxClients, limits.Headroom)
	for _, engine := range profileEngines {
		text += fmt.Sprintf("\n%s · %d", freeEngineName(engine), byEngine[engine])
	}
	plans := make([]string, 0, len(byPlan))
	for name := range byPlan {
		plans = append(plans, name)
	}
	slices.Sort(plans)
	if len(plans) > 0 {
		text += "\n\n" + t.tr("Issued by plan", "حساب‌های صادرشده به تفکیک پلن")
		for _, p := range plans {
			text += fmt.Sprintf("\n%s · %d", p, byPlan[p])
		}
	}
	_, e = t.notify(ctx, t.config(), text, telegramKeyboard{Rows: [][]telegramButton{{t.button("Back", "بازگشت", "free:manage")}}})
	return e
}

func (t *Telegram) freeUserSelect(ctx context.Context, id string) error {
	c, e := t.h.store.Get(id)
	if e != nil {
		return e
	}
	if c.InstallerProbe {
		return ErrNotFound
	}
	if i := slices.Index(t.freeDeleteSelected, id); i >= 0 {
		t.freeDeleteSelected = slices.Delete(t.freeDeleteSelected, i, i+1)
	} else {
		if len(t.freeDeleteSelected) >= 20 {
			return errors.New("select at most 20 accounts")
		}
		t.freeDeleteSelected = append(t.freeDeleteSelected, id)
	}
	page, query, active := 0, "", false
	if d := t.conversation; d != nil {
		query, active = d.Query, d.ActiveOnly
		page = d.ListPage
	}
	return t.userPage(ctx, page, query, active)
}

func (t *Telegram) confirmFreeDeletion(ctx context.Context) error {
	if len(t.freeDeleteSelected) == 0 {
		return errors.New("select at least one account")
	}
	c := telegramConfirmation{Action: "free-delete-users", FreeIDs: slices.Clone(t.freeDeleteSelected)}
	lines := []string{t.tr("Permanently delete these accounts, their connection keys and usage history?", "این حساب‌ها، کلید اتصال و سابقهٔ مصرفشان برای همیشه حذف شوند؟")}
	for _, id := range c.FreeIDs {
		a, e := t.h.store.Get(id)
		if e != nil || a.InstallerProbe {
			return ErrNotFound
		}
		c.FreeRevisions = append(c.FreeRevisions, a.Revision)
		lines = append(lines, a.Name+" · "+a.Zone)
	}
	return t.confirm(ctx, c, strings.Join(lines, "\n"))
}

func (t *Telegram) deleteFreeSelection(ctx context.Context, ids []string, revisions []uint64) error {
	if e := t.h.store.deleteMany(ids, revisions); e != nil {
		return e
	}
	for _, id := range ids {
		t.h.manager.Disconnect(id)
	}
	t.freeDeleteSelected = nil
	if e := t.h.manager.Reconcile(); e != nil {
		return errors.New("accounts deleted; runtime synchronization will be retried")
	}
	_, e := t.notify(ctx, t.config(), fmt.Sprintf(t.tr("Deleted %d account(s).", "%d حساب حذف شد."), len(ids)), t.homeKeyboard())
	return e
}
