package panel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	bolt "go.etcd.io/bbolt"
)

var freeBroadcastKey = []byte("telegram_free_broadcast")

type freeBroadcast struct {
	ID         string
	Bot        [32]byte
	Segment    string
	Text       string
	Recipients []string
	Next       int
	InFlight   bool
	Sent       int
	Failed     int
	Skipped    int
	Uncertain  int
	Stopped    bool
}

func (b freeBroadcast) active() bool {
	return b.ID != "" && !b.Stopped && (b.Next < len(b.Recipients) || b.InFlight)
}

func readFreeBroadcast(tx *bolt.Tx) (freeBroadcast, error) {
	var b freeBroadcast
	if raw := tx.Bucket(meta).Get(freeBroadcastKey); raw != nil {
		e := json.Unmarshal(raw, &b)
		return b, e
	}
	return b, nil
}

func (s *Store) freeBroadcast() (b freeBroadcast, e error) {
	e = s.db.View(func(tx *bolt.Tx) error { b, e = readFreeBroadcast(tx); return e })
	return
}

func (s *Store) changeFreeBroadcast(change func(*freeBroadcast) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, e := readFreeBroadcast(tx)
		if e != nil {
			return e
		}
		if e = change(&b); e != nil {
			return e
		}
		return putJSON(tx.Bucket(meta), freeBroadcastKey, b)
	})
}

func (s *Store) freeAudience(segment string, now time.Time) (ids []string, e error) {
	e = s.db.View(func(tx *bolt.Tx) error {
		members := tx.Bucket(meta).Bucket(freeMembers)
		if members == nil {
			return nil
		}
		return members.ForEach(func(key, raw []byte) error {
			var m freeMember
			if e := json.Unmarshal(raw, &m); e != nil {
				return e
			}
			match, e := matchesFreeAudience(tx, key, m, segment, now)
			if match {
				ids = append(ids, string(key))
			}
			return e
		})
	})
	return
}

func matchesFreeAudience(tx *bolt.Tx, key []byte, m freeMember, segment string, now time.Time) (bool, error) {
	if !m.Started || (segment != "stats" && (m.OptOut || m.Blocked)) {
		return false, nil
	}
	switch segment {
	case "stats", "all":
		return true, nil
	case "fa", "en":
		return m.Language == segment, nil
	case "active":
		requests := tx.Bucket(meta).Bucket(freeRequests)
		if requests == nil || requests.Get(key) == nil {
			return false, nil
		}
		var r telegramFreeRequest
		if e := json.Unmarshal(requests.Get(key), &r); e != nil {
			return false, e
		}
		a, e := getClient(tx, r.AccountID)
		if errors.Is(e, ErrNotFound) {
			return false, nil
		}
		return e == nil && a.Status(now) == "active", e
	default:
		return false, ErrNotFound
	}
}

func (t *Telegram) freeBroadcastMenu(ctx context.Context, action, value string) error {
	if action == "announce" {
		if value != "all" && value != "active" && value != "fa" && value != "en" {
			return ErrNotFound
		}
		return t.freePrompt(ctx, "broadcast:"+value, 0, nil,
			t.tr("Reply with the announcement (max 2,400 bytes). Review and confirm before sending.", "متن پیام همگانی را بفرستید (حداکثر ۲۴۰۰ بایت). پیش از ارسال پیش‌نمایش را تأیید کنید."))
	}
	b, e := t.h.store.freeBroadcast()
	if e != nil {
		return e
	}
	if action == "broadcaststop" {
		if e = t.h.store.changeFreeBroadcast(func(b *freeBroadcast) error {
			if b.ID == "" || b.ID != value {
				return ErrConflict
			}
			b.Stopped = true
			return nil
		}); e != nil {
			return e
		}
		return t.freeBroadcastMenu(ctx, "broadcast", "")
	}
	k := telegramKeyboard{Rows: [][]telegramButton{
		{t.button("All subscribers", "همهٔ مخاطبان", "free:announce:all"), t.button("Active owners", "صاحبان حساب فعال", "free:announce:active")},
		{t.button("Persian", "فارسی", "free:announce:fa"), t.button("English", "انگلیسی", "free:announce:en")},
	}}
	text := t.tr("Choose a broadcast audience.", "مخاطبان پیام همگانی را انتخاب کنید.")
	if b.ID != "" {
		text += fmt.Sprintf(t.tr("\nSent %d · Failed %d · Skipped %d · Uncertain %d · Pending %d",
			"\nارسال %d · خطا %d · رد شده %d · نامشخص %d · باقی‌مانده %d"),
			b.Sent, b.Failed, b.Skipped, b.Uncertain, len(b.Recipients)-b.Next)
		if b.active() {
			k.Rows = [][]telegramButton{{t.button("Refresh", "تازه‌سازی", "free:broadcast"), t.button("Stop broadcast", "توقف پیام همگانی", "free:broadcaststop:"+b.ID)}}
		}
	}
	k.Rows = append(k.Rows, []telegramButton{t.button("Back", "بازگشت", "free:manage")})
	_, e = t.notify(ctx, t.config(), text, k)
	return e
}

func (t *Telegram) previewFreeBroadcast(ctx context.Context, segment, text string) error {
	if text == "" || len(text) > 2400 {
		return errors.New("broadcast text must contain between 1 and 2,400 bytes")
	}
	ids, e := t.h.store.freeAudience(segment, t.now())
	if e != nil {
		return e
	}
	if len(ids) == 0 {
		return errors.New("no broadcast recipients")
	}
	return t.confirm(ctx, telegramConfirmation{Action: "free-broadcast", ID: segment, FreeIDs: ids, FreeText: text},
		fmt.Sprintf(t.tr("Broadcast to %d recipient(s)\n\n%s", "پیام همگانی برای %d مخاطب\n\n%s"), len(ids), text))
}

func (t *Telegram) startFreeBroadcast(ctx context.Context, text string, ids []string, segment string) error {
	c := t.config()
	if !c.FreeAccess {
		return errFreePaused
	}
	bot := sha256.Sum256([]byte(c.Token))
	if e := t.h.store.db.Update(func(tx *bolt.Tx) error {
		catalog, e := readFreeCatalog(tx)
		if e != nil {
			return e
		}
		if catalog.RestoreHold {
			return errFreePaused
		}
		b, e := readFreeBroadcast(tx)
		if e != nil {
			return e
		}
		if b.Bot == bot && (b.active() || b.InFlight) {
			return errors.New("another broadcast is in progress")
		}
		b = freeBroadcast{ID: token()[:10], Bot: bot, Segment: segment, Text: text, Recipients: ids}
		return putJSON(tx.Bucket(meta), freeBroadcastKey, b)
	}); e != nil {
		return e
	}
	return t.freeBroadcastMenu(ctx, "broadcast", "")
}

func inspectFreeBroadcast(tx *bolt.Tx) error {
	b, e := readFreeBroadcast(tx)
	if e != nil || b.ID == "" {
		return e
	}
	accounted := b.Sent + b.Failed + b.Skipped + b.Uncertain
	if b.InFlight {
		accounted++
	}
	if !oneOf(b.Segment, "all", "active", "fa", "en") || len(b.Text) < 1 || len(b.Text) > 2400 || len(b.Recipients) < 1 ||
		len(b.Recipients) > freeMemberLimit || b.Next < 0 || b.Next > len(b.Recipients) || b.Next != accounted ||
		b.Sent < 0 || b.Failed < 0 || b.Skipped < 0 || b.Uncertain < 0 {
		return errors.New("invalid Telegram broadcast record")
	}
	seen := make(map[string]bool)
	for _, id := range b.Recipients {
		if !validFreeChatID(id) || seen[id] {
			return errors.New("invalid Telegram broadcast recipient")
		}
		seen[id] = true
	}
	return nil
}

type telegramFreeNotice struct {
	token string
	id    int64
}

func (t *Telegram) queueFreeNotice(c TelegramConfig, id int64) {
	if id <= 0 || !c.FreeAccess {
		return
	}
	select {
	case t.freeNotices <- telegramFreeNotice{c.Token, id}:
	default:
	}
}

func (t *Telegram) runFreeDelivery(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	prune := time.NewTicker(time.Minute)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case notice := <-t.freeNotices:
			c := t.config()
			if c.Enabled && c.FreeAccess && c.Token == notice.token {
				m, e := t.h.store.freeMember(notice.id)
				if e == nil && !m.Blocked {
					c.ChatID = notice.id
					job, cancel := context.WithTimeout(withFreeLane(ctx, freeBroadcastLane), 2*time.Second)
					_, _ = t.sendText(job, c, "Request reviewed. Use /free_status.\nدرخواست بررسی شد. نتیجه را با /free_status ببینید.", nil)
					cancel()
				}
			}
		case <-tick.C:
			t.sendFreeBroadcast(ctx)
		case <-prune.C:
			_ = t.h.store.pruneFreeRequests(t.now())
		}
	}
}

func (t *Telegram) sendFreeBroadcast(ctx context.Context) {
	c := t.config()
	if !c.Enabled || !c.FreeAccess || ctx.Err() != nil {
		return
	}
	s := t.h.store
	catalog, e := s.freeCatalog()
	if e != nil || catalog.RestoreHold {
		return
	}
	b, e := s.freeBroadcast()
	if e != nil || (!b.active() && !b.InFlight) {
		return
	}
	if b.Bot != sha256.Sum256([]byte(c.Token)) {
		_ = s.changeFreeBroadcast(func(current *freeBroadcast) error {
			if current.ID != b.ID {
				return ErrConflict
			}
			if current.InFlight {
				current.InFlight = false
				current.Uncertain++
			}
			current.Stopped = true
			return nil
		})
		return
	}
	job, cancel := context.WithTimeout(withFreeLane(ctx, freeBroadcastLane), 4*time.Second)
	defer cancel()
	if e = t.waitAPI(job, false); e != nil {
		return
	}
	var id int64
	if e = s.changeFreeBroadcast(func(current *freeBroadcast) error {
		if current.ID != b.ID {
			return ErrConflict
		}
		if current.InFlight {
			// An interrupted send might already have reached Telegram.
			current.InFlight = false
			current.Uncertain++
		}
		if current.active() {
			id, _ = strconv.ParseInt(current.Recipients[current.Next], 10, 64)
			current.Next++
			current.InFlight = true
		}
		return nil
	}); e != nil || id <= 0 {
		return
	}
	state := t.config()
	recipient := false
	memberErr := s.db.View(func(tx *bolt.Tx) error {
		key := []byte(strconv.FormatInt(id, 10))
		m, e := readFreeMember(tx.Bucket(meta).Bucket(freeMembers), key)
		if e != nil {
			return e
		}
		recipient, e = matchesFreeAudience(tx, key, m, b.Segment, t.now())
		return e
	})
	skipped := memberErr != nil || !recipient || !state.Enabled || !state.FreeAccess || state.Token != c.Token
	if !skipped {
		c.ChatID = id
		_, e = t.sendText(job, c, b.Text, nil)
	}
	_ = s.changeFreeBroadcast(func(current *freeBroadcast) error {
		if current.ID != b.ID || !current.InFlight {
			return ErrConflict
		}
		current.InFlight = false
		var api *telegramAPIError
		switch {
		case skipped:
			current.Skipped++
		case e == nil:
			current.Sent++
		case errors.As(e, &api) && api.Code == 429:
			current.Next--
		case api != nil:
			current.Failed++
		default:
			current.Uncertain++
		}
		return nil
	})
}
