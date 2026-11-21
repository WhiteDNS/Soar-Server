package panel

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type freeLane uint8

const (
	freeAdmissionLane freeLane = iota + 1
	freeOwnerLane
	freeBroadcastLane
	freeMenuLane
	freeBusyLane
)

type freeLaneKey struct{}
type freePublicConfigKey struct{}

const (
	freeMenuCapacity = 600
	freeMenuWorkers  = 12
	freeMenuLifetime = 90 * time.Second
)

type telegramFreeTraffic struct {
	received, admitted, merged, rejected, sent, failed, limited atomic.Uint64
}

func withFreeLane(ctx context.Context, lane freeLane) context.Context {
	return context.WithValue(ctx, freeLaneKey{}, lane)
}

type telegramFreeSend struct{ next, retry time.Time }

type freePublicRate struct {
	recent, menuRecent map[int64]time.Time
	all                []time.Time
}

func freeReadAction(action string) bool {
	return slices.Contains([]string{"menu", "status", "config", "qr", "link", "guide", "terms", "language", "lang", "opt"}, action)
}

func (t *Telegram) allowFreeJob(chat int64, now time.Time, action string, owned, menu bool, limits freeLimits) bool {
	t.freeMu.Lock()
	defer t.freeMu.Unlock()
	index, maximum := 0, limits.ApplicationsMinute
	if action == "reply" {
		maximum += 4
	}
	if owned {
		index, maximum = 1, limits.OwnerMinute
	}
	rate := &t.freeRate[index]
	rate.all = slices.DeleteFunc(rate.all, func(at time.Time) bool { return !now.Before(at.Add(time.Minute)) })
	recent, interval := &rate.recent, time.Duration(limits.ActionSeconds)*time.Second
	if menu {
		recent, interval = &rate.menuRecent, time.Duration(limits.MenuDelayMS)*time.Millisecond
	}
	if len(rate.all) >= maximum || now.Before((*recent)[chat].Add(interval)) {
		return false
	}
	for id, at := range *recent {
		if !now.Before(at.Add(interval)) {
			delete(*recent, id)
		}
	}
	if *recent == nil {
		*recent = make(map[int64]time.Time)
	}
	(*recent)[chat] = now
	rate.all = append(rate.all, now)
	return true
}

func (t *Telegram) allowFreeChallenge(id int64, now time.Time, maximum int) bool {
	t.freeMu.Lock()
	defer t.freeMu.Unlock()
	for key, entries := range t.freeAttempts {
		entries = slices.DeleteFunc(entries, func(at time.Time) bool { return !now.Before(at.Add(time.Hour)) })
		if len(entries) == 0 {
			delete(t.freeAttempts, key)
		} else {
			t.freeAttempts[key] = entries
		}
	}
	for key, challenge := range t.freeChallenges {
		if !now.Before(challenge.Expires) {
			delete(t.freeChallenges, key)
		}
	}
	if len(t.freeAttempts[id]) >= maximum {
		return false
	}
	if t.freeAttempts == nil {
		t.freeAttempts = make(map[int64][]time.Time)
	}
	t.freeAttempts[id] = append(t.freeAttempts[id], now)
	return true
}

func (t *Telegram) clearFreeChallenge(id int64) {
	t.freeMu.Lock()
	defer t.freeMu.Unlock()
	if id == 0 {
		t.freeChallenges = nil
	} else {
		delete(t.freeChallenges, id)
	}
}

type telegramFreeJob struct {
	config   TelegramConfig
	revision uint64
	from     telegramUser
	update   telegramUpdate
	reply    int64
	expires  time.Time
}

type telegramFreeMenuSlot struct {
	action, value string
	delay         time.Duration
	cooldown      time.Time
}

func (t *Telegram) runFreeWorkers(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	t.opMu.Lock()
	t.freeNotices = make(chan telegramFreeNotice, maxFreePending)
	t.freeReplies = make(chan telegramFreeJob, 64)
	t.freeReplyPending = nil
	t.freeBusy = make(chan telegramFreeJob, 8)
	t.freeMenus = make(map[int64]telegramFreeMenuSlot)
	for i := range t.freeReserved {
		t.freeReserved[i] = make(map[int64]struct{})
	}
	wg.Go(func() { t.runFreeDelivery(ctx) })
	wg.Go(func() { t.runFreeBusy(ctx) })
	for i := range t.freeQueues {
		capacity, workers := maxFreeActionQueue, 1
		if i == 2 {
			capacity, workers = maxFreeMenuQueue, freeMenuWorkers
		}
		queue := make(chan telegramFreeJob, capacity)
		t.freeQueues[i] = queue
		var replies <-chan telegramFreeJob
		if i == 0 {
			replies = t.freeReplies
		}
		for range workers {
			wg.Go(func() {
				for ctx.Err() == nil {
					var job telegramFreeJob
					select {
					case job = <-replies:
					default:
						select {
						case <-ctx.Done():
							return
						case job = <-replies:
						case job = <-queue:
						}
					}
					t.mu.Lock()
					current, revision := t.cfg, t.freeRevision
					t.mu.Unlock()
					jobCtx := context.WithValue(ctx, freePublicConfigKey{}, job.revision)
					if job.revision == revision && current.Enabled && current.FreeAccess &&
						current.administratorRole(job.from.ID) == "" {
						if i == 2 {
							menuCtx, cancel := context.WithDeadline(withFreeLane(jobCtx, freeMenuLane), job.expires)
							if menuCtx.Err() == nil {
								t.handleFreeUpdate(menuCtx, current, job.from, job.update)
							} else {
								t.freeTraffic.rejected.Add(1)
							}
							cancel()
						} else {
							if i == 0 {
								jobCtx = withFreeLane(jobCtx, freeAdmissionLane)
							}
							t.handleFreeUpdate(jobCtx, current, job.from, job.update)
						}
					}
					t.freeMu.Lock()
					if job.reply != 0 {
						delete(t.freeReplyPending, job.from.ID)
					} else if i == 2 {
						slot := t.freeMenus[job.from.ID]
						slot.cooldown = time.Now().Add(slot.delay)
						t.freeMenus[job.from.ID] = slot
					} else {
						delete(t.freeReserved[i], job.from.ID)
					}
					t.freeMu.Unlock()
				}
			})
		}
	}
	t.opMu.Unlock()
	return func() {
		cancel()
		wg.Wait()
	}
}

func (t *Telegram) runFreeBusy(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-t.freeBusy:
			guarded := context.WithValue(ctx, freePublicConfigKey{}, job.revision)
			busy, cancel := context.WithTimeout(withFreeLane(guarded, freeBusyLane), 3*time.Second)
			member, err := t.h.store.freeMember(job.from.ID)
			if err == nil && !member.Blocked {
				c := job.config
				c.ChatID = job.from.ID
				message := "The bot is busy. Try again in a minute."
				if member.Language == "fa" || member.Language == "" && strings.HasPrefix(job.from.Language, "fa") {
					message = "ربات شلوغ است. یک دقیقه دیگر دوباره امتحان کنید."
				}
				_, _ = t.sendText(busy, c, message, nil)
			}
			cancel()
		}
	}
}

func (t *Telegram) rejectFreeJob(job telegramFreeJob) {
	t.freeTraffic.rejected.Add(1)
	select {
	case t.freeBusy <- job:
	default:
	}
}

func freeMenuAction(action string, u telegramUpdate) bool {
	return freeReadAction(action) ||
		action == "request" && u.Callback != nil
}

func (t *Telegram) queueFreeMenu(job telegramFreeJob, action, value string, limits freeLimits) {
	t.freeMu.Lock()
	defer t.freeMu.Unlock()
	now := time.Now()
	if slot, ok := t.freeMenus[job.from.ID]; ok {
		if (slot.cooldown.IsZero() || slot.cooldown.After(now)) && slot.action == action && slot.value == value {
			t.freeTraffic.merged.Add(1)
			return
		}
		if slot.cooldown.IsZero() {
			t.rejectFreeJob(job)
			return
		}
	}
	for id, slot := range t.freeMenus {
		if !slot.cooldown.IsZero() && !slot.cooldown.After(now) {
			delete(t.freeMenus, id)
		}
	}
	job.expires = now.Add(freeMenuLifetime)
	if len(t.freeQueues[2]) >= limits.MenuQueue {
		t.rejectFreeJob(job)
		return
	}
	select {
	case t.freeQueues[2] <- job:
		t.freeMenus[job.from.ID] = telegramFreeMenuSlot{action: action, value: value, delay: time.Duration(limits.MenuDelayMS) * time.Millisecond}
		t.freeTraffic.admitted.Add(1)
	default:
		t.rejectFreeJob(job)
	}
}

func (t *Telegram) logFreeTraffic() {
	m := &t.freeTraffic
	received, admitted, merged, rejected := m.received.Swap(0), m.admitted.Swap(0), m.merged.Swap(0), m.rejected.Swap(0)
	sent, failed, limited := m.sent.Swap(0), m.failed.Swap(0), m.limited.Swap(0)
	if received+sent+failed+limited != 0 {
		slog.Info("Telegram public traffic", "received", received, "queued", admitted, "merged", merged,
			"rejected", rejected, "sent", sent, "failed", failed, "rate_limited", limited)
	}
}

func (t *Telegram) dispatchFree(ctx context.Context, c TelegramConfig, from telegramUser, u telegramUpdate) {
	action, value := freeAction(u)
	if action == "" {
		return
	}
	if t.freeQueues[0] == nil {
		t.handleFreeUpdate(ctx, c, from, u)
		return
	}
	t.freeTraffic.received.Add(1)
	index := 0
	job := telegramFreeJob{config: c, revision: t.freeRevision, from: from, update: u}
	if action == "reply" && t.freeReplies != nil {
		t.freeMu.Lock()
		defer t.freeMu.Unlock()
		challenge := t.freeChallenges[from.ID]
		if challenge.Prompt == 0 || u.Message.Reply.ID != challenge.Prompt || !t.now().Before(challenge.Expires) {
			t.freeTraffic.rejected.Add(1)
			return
		}
		if t.freeReplyPending[from.ID] != 0 {
			t.freeTraffic.merged.Add(1)
			return
		}
		job.reply = challenge.Prompt
		select {
		case t.freeReplies <- job:
			if t.freeReplyPending == nil {
				t.freeReplyPending = make(map[int64]int64)
			}
			t.freeReplyPending[from.ID] = job.reply
			t.freeTraffic.admitted.Add(1)
		default:
			t.rejectFreeJob(job)
		}
		return
	}
	catalog, err := t.h.store.freeCatalog()
	if err != nil {
		t.freeTraffic.failed.Add(1)
		return
	}
	limits := catalog.limits()
	capacity := limits.ApplicationQueue
	if freeReadAction(action) && t.h.store.hasFreeOwner(from.ID) {
		index = 1
		capacity = limits.OwnerQueue
	} else if freeMenuAction(action, u) {
		t.queueFreeMenu(job, action, value, limits)
		return
	}
	t.freeMu.Lock()
	defer t.freeMu.Unlock()
	if _, exists := t.freeReserved[index][from.ID]; exists {
		t.freeTraffic.merged.Add(1)
		return
	}
	if len(t.freeQueues[index]) >= capacity {
		t.rejectFreeJob(job)
		return
	}
	select {
	case t.freeQueues[index] <- job:
		t.freeReserved[index][from.ID] = struct{}{}
		t.freeTraffic.admitted.Add(1)
	default:
		t.rejectFreeJob(job)
	}
}

func (t *Telegram) freeAccountReady(account Client) bool {
	m := t.h.manager
	m.mu.RLock()
	defer m.mu.RUnlock()
	rt := m.accounts[account.ID]
	// Cotten and MasterDNS start on the first query; the route must exist first.
	return rt != nil && rt.ctx.Err() == nil && rt.client.PublicKey == account.PublicKey && rt.client.Engines == account.Engines
}

func (t *Telegram) canDeliverFree(c TelegramConfig, id int64, account Client) bool {
	live := t.config()
	if !live.Enabled || !live.FreeAccess || live.Token != c.Token {
		return false
	}
	member, err := t.h.store.freeMember(id)
	if err != nil || member.Blocked {
		return false
	}
	now := t.now()
	req, err := t.h.store.freeRequest(id, now)
	if err != nil || req.AccountID != account.ID {
		return false
	}
	current, err := t.h.store.Get(account.ID)
	return err == nil && current.Revision == account.Revision && current.Status(now) == "active" && t.freeAccountReady(current)
}

func freeEngineName(engine string) string {
	switch engine {
	case "soar":
		return "Soar"
	case "cotten":
		return "CottenDNS"
	case "masterdns":
		return "MasterDNS"
	default:
		return engine
	}
}

func freePlanSummary(p freePlan, tr func(string, string) string) string {
	engines := ""
	for _, engine := range engineSelections[p.Input.Engines] {
		if engines != "" {
			engines += " + "
		}
		engines += freeEngineName(engine)
	}
	details := fmt.Sprintf(tr("%.2f GiB · %d days · %.1f KiB/s · %d streams",
		"%.2f گیبی‌بایت · %d روز · %.1f کیبی‌بایت بر ثانیه · %d اتصال هم‌زمان"),
		float64(p.Input.QuotaBytes)/(1<<30), p.Days, float64(p.Input.RateBytes)/1024, p.Input.StreamLimit)
	sessions := map[string]*int{"soar": p.Input.SoarSessionLimit, "cotten": p.Input.CottenSessionLimit, "masterdns": p.Input.MasterDNSSessionLimit}
	for _, engine := range engineSelections[p.Input.Engines] {
		if limit := sessions[engine]; limit != nil {
			details += fmt.Sprintf(tr("\n%s sessions: %d", "\nنشست %s: %d"), freeEngineName(engine), *limit)
		}
	}
	if tr("en", "fa") == "fa" {
		details = telegramPersianDigits.Replace(details)
	}
	text := p.Input.Name + " · " + engines + "\n" + details
	if note := p.Input.Note; note != "" {
		text += "\n" + note
	}
	return text
}
