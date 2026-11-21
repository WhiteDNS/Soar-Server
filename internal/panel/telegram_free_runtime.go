package panel

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

type freeLane uint8

const (
	freeAdmissionLane freeLane = iota + 1
	freeOwnerLane
	freeBroadcastLane
)

type freeLaneKey struct{}

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

func (t *Telegram) allowFreeJob(chat int64, now time.Time, action string, owned, menu bool) bool {
	t.freeMu.Lock()
	defer t.freeMu.Unlock()
	index, maximum := 0, freeTrafficLimit-4
	if action == "reply" {
		maximum = freeTrafficLimit
	}
	if owned {
		index, maximum = 1, 30
	}
	rate := &t.freeRate[index]
	rate.all = slices.DeleteFunc(rate.all, func(at time.Time) bool { return !now.Before(at.Add(time.Minute)) })
	recent, interval := &rate.recent, 3*time.Second
	if menu {
		recent, interval = &rate.menuRecent, 750*time.Millisecond
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

func (t *Telegram) allowFreeChallenge(id int64, now time.Time) bool {
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
	if len(t.freeAttempts[id]) >= 3 {
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
	config TelegramConfig
	from   telegramUser
	update telegramUpdate
	reply  int64
}

func (t *Telegram) runFreeWorkers(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	t.opMu.Lock()
	t.freeNotices = make(chan telegramFreeNotice, freeQueueLimit)
	t.freeReplies = make(chan telegramFreeJob, 12)
	wg.Go(func() { t.runFreeDelivery(ctx) })
	for i := range t.freeQueues {
		queue := make(chan telegramFreeJob, 24)
		t.freeQueues[i] = queue
		var replies <-chan telegramFreeJob
		if i == 0 {
			replies = t.freeReplies
		}
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
				current := t.config()
				if current.Enabled && current.FreeAccess && current.Token == job.config.Token && current.ChatID == job.config.ChatID &&
					current.administratorRole(job.from.ID) == "" {
					t.handleFreeUpdate(ctx, current, job.from, job.update)
				}
				if job.reply != 0 {
					t.freeMu.Lock()
					delete(t.freeReplyPending, job.from.ID)
					t.freeMu.Unlock()
				}
			}
		})
	}
	t.opMu.Unlock()
	return func() {
		cancel()
		wg.Wait()
	}
}

func (t *Telegram) dispatchFree(ctx context.Context, c TelegramConfig, from telegramUser, u telegramUpdate) {
	action, _ := freeAction(u)
	if action == "" {
		return
	}
	queue := t.freeQueues[0]
	job := telegramFreeJob{config: c, from: from, update: u}
	if action == "reply" && t.freeReplies != nil {
		t.freeMu.Lock()
		defer t.freeMu.Unlock()
		challenge := t.freeChallenges[from.ID]
		if challenge.Prompt == 0 || u.Message.Reply.ID != challenge.Prompt || !t.now().Before(challenge.Expires) ||
			t.freeReplyPending[from.ID] != 0 {
			return
		}
		job.reply = challenge.Prompt
		select {
		case t.freeReplies <- job:
			if t.freeReplyPending == nil {
				t.freeReplyPending = make(map[int64]int64)
			}
			t.freeReplyPending[from.ID] = job.reply
		default:
		}
		return
	} else if freeReadAction(action) && t.h.store.hasFreeOwner(from.ID) {
		queue = t.freeQueues[1]
	}
	if queue != nil {
		select {
		case queue <- job:
		default:
		}
		return
	}
	t.handleFreeUpdate(ctx, c, from, u)
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
