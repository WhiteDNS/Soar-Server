package panel

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	freeQueueLimit     = 20
	freeAdmissionLimit = 40
	freeDailyLimit     = 10
	freeQuotaBytes     = 1 << 30
	freeCooldown       = 24 * time.Hour
	freeOwnedLimit     = 100
	freePanelHeadroom  = 20
	freeStreamBudget   = 512
)

var (
	freeRequests      = []byte("telegram_free_requests")
	errFreeQueueFull  = errors.New("free configuration queue is full")
	errFreeDailyLimit = errors.New("daily free configuration limit reached")
)

type telegramFreeRequest struct {
	Key       string
	Reason    string `json:",omitempty"`
	AccountID string `json:",omitempty"`
	Reviewed  bool
	At        time.Time
	Plan      *freePlan `json:",omitempty"`
	DecidedAt time.Time `json:",omitzero"`
	RemovedAt time.Time `json:",omitzero"`
	Cancelled bool      `json:",omitempty"`
	Linked    bool      `json:",omitempty"`
	Notified  bool      `json:",omitempty"`
}

func (r telegramFreeRequest) decisionTime() time.Time {
	if r.DecidedAt.IsZero() {
		return r.At
	}
	return r.DecidedAt
}

func (r telegramFreeRequest) plan() freePlan {
	if r.Plan != nil {
		return *r.Plan
	}
	return defaultFreePlan()
}

func (s *Store) freeRequest(chatID int64, now time.Time) (req telegramFreeRequest, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(meta).Bucket(freeRequests)
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(strconv.FormatInt(chatID, 10)))
		if raw == nil {
			return nil
		}
		if e := json.Unmarshal(raw, &req); e != nil {
			return e
		}
		if expiredFreeRequest(tx, req, now) {
			req = telegramFreeRequest{}
		}
		return nil
	})
	return
}

func expiredFreeRequest(tx *bolt.Tx, req telegramFreeRequest, now time.Time) bool {
	return !now.Before(maxTime(req.decisionTime(), req.RemovedAt).Add(freeCooldown)) &&
		(req.AccountID == "" || tx.Bucket(clients).Get([]byte(req.AccountID)) == nil)
}

func (s *Store) submitFreePlan(chatID int64, nonce, reason string, plan freePlan, now time.Time) error {
	if chatID <= 0 || len(nonce) != 12 || !validFreeReason(reason) {
		return errors.New("invalid free configuration request")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		catalog, e := readFreeCatalog(tx)
		if e != nil {
			return e
		}
		if catalog.Paused || catalog.RestoreHold {
			return errFreePaused
		}
		p, ok := catalog.plan(plan.ID)
		if !ok || p.Revision != plan.Revision || !catalog.offers(p) {
			return errFreePlanUnavailable
		}
		b, e := tx.Bucket(meta).CreateBucketIfNotExists(freeRequests)
		if e != nil {
			return e
		}
		chat := strconv.FormatInt(chatID, 10)
		blocked, e := blockedFreeMember(tx, []byte(chat))
		if e != nil {
			return e
		}
		if blocked {
			return errFreePaused
		}
		var outdated [][]byte
		pending, recent := 0, 0
		e = b.ForEach(func(k, raw []byte) error {
			var req telegramFreeRequest
			if e := json.Unmarshal(raw, &req); e != nil {
				return e
			}
			if now.Before(req.At.Add(freeCooldown)) {
				recent++
			}
			switch {
			case expiredFreeRequest(tx, req, now):
				outdated = append(outdated, append([]byte(nil), k...))
			case string(k) == chat:
				return ErrConflict
			case !req.Reviewed:
				pending++
			}
			return nil
		})
		if e != nil {
			return e
		}
		if pending >= freeQueueLimit || recent >= freeAdmissionLimit {
			return errFreeQueueFull
		}
		for _, key := range outdated {
			if e := b.Delete(key); e != nil {
				return e
			}
		}
		return putJSON(b, []byte(chat), telegramFreeRequest{Key: chat + ":" + nonce, Reason: reason, At: now, Plan: &p})
	})
}

func (s *Store) freePending(now time.Time) (pending []telegramFreeRequest, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(meta).Bucket(freeRequests)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var req telegramFreeRequest
			if e := json.Unmarshal(raw, &req); e != nil {
				return e
			}
			if !req.Reviewed && !expiredFreeRequest(tx, req, now) {
				pending = append(pending, req)
			}
			return nil
		})
	})
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].At.Before(pending[j].At) })
	return pending, err
}

func (s *Store) freeQueue(now time.Time) (int, telegramFreeRequest, error) {
	pending, e := s.freePending(now)
	if len(pending) == 0 {
		return 0, telegramFreeRequest{}, e
	}
	return len(pending), pending[0], e
}

func (s *Store) approveFree(key string, now time.Time) (Client, error) {
	chat, _, _ := strings.Cut(key, ":")
	id, e := strconv.ParseInt(chat, 10, 64)
	if e != nil || id < 1 {
		return Client{}, ErrNotFound
	}
	req, e := s.freeRequest(id, now)
	if e != nil {
		return Client{}, e
	}
	if req.Key != key || req.Reviewed {
		return Client{}, ErrNotFound
	}
	return s.create(req.plan().account(chat, now), false, func(tx *bolt.Tx, account Client) error {
		return reviewFree(tx, key, account.ID, now)
	})
}

func (s *Store) cancelFree(id int64, now time.Time) (bool, error) {
	cancelled := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(meta).Bucket(freeRequests)
		if b == nil {
			return nil
		}
		key := []byte(strconv.FormatInt(id, 10))
		raw := b.Get(key)
		if raw == nil {
			return nil
		}
		var req telegramFreeRequest
		if e := json.Unmarshal(raw, &req); e != nil {
			return e
		}
		if req.Reviewed || expiredFreeRequest(tx, req, now) {
			return nil
		}
		req.Reviewed, req.Cancelled, req.DecidedAt, req.Reason = true, true, now, ""
		cancelled = true
		return putJSON(b, key, req)
	})
	return cancelled, err
}

func reviewFree(tx *bolt.Tx, key, accountID string, now time.Time) error {
	chatID, _, _ := strings.Cut(key, ":")
	b := tx.Bucket(meta).Bucket(freeRequests)
	if b == nil || b.Get([]byte(chatID)) == nil {
		return ErrNotFound
	}
	var req telegramFreeRequest
	if e := json.Unmarshal(b.Get([]byte(chatID)), &req); e != nil {
		return e
	}
	if req.Key != key || req.Reviewed || expiredFreeRequest(tx, req, now) {
		return ErrNotFound
	}
	if accountID != "" {
		blocked, e := blockedFreeMember(tx, []byte(chatID))
		if e != nil {
			return e
		}
		if blocked {
			return errFreePaused
		}
		catalog, e := readFreeCatalog(tx)
		if e != nil {
			return e
		}
		if catalog.RestoreHold {
			return errFreePaused
		}
		// Eligibility uses the current policy; approved limits come from the saved request.
		current, ok := catalog.plan(req.plan().ID)
		snapshot := req.plan()
		snapshot.Published = current.Published
		if !ok || !catalog.offers(current) || !catalog.offers(snapshot) {
			return errFreePlanUnavailable
		}
		if e = checkFreeCapacity(tx, b, now, snapshot.Input.StreamLimit, ""); e != nil {
			return e
		}
	}
	req.Reviewed, req.DecidedAt, req.Reason, req.AccountID = true, now, "", accountID
	return putJSON(b, []byte(chatID), req)
}

func markFreeAccountRemoved(tx *bolt.Tx, id string, now time.Time) error {
	b := tx.Bucket(meta).Bucket(freeRequests)
	if b == nil {
		return nil
	}
	var key []byte
	var matched telegramFreeRequest
	if err := b.ForEach(func(k, raw []byte) error {
		var req telegramFreeRequest
		if e := json.Unmarshal(raw, &req); e != nil {
			return e
		}
		if req.AccountID == id {
			key, matched = append([]byte(nil), k...), req
		}
		return nil
	}); err != nil || key == nil {
		return err
	}
	matched.RemovedAt = now
	return putJSON(b, key, matched)
}

func checkFreeCapacity(tx *bolt.Tx, b *bolt.Bucket, now time.Time, streams int, replacing string) error {
	accounts := tx.Bucket(clients)
	if replacing == "" && accounts.Stats().KeyN >= MaxClients-freePanelHeadroom {
		return errFreeCapacity
	}
	issued, owned, totalStreams := 0, 0, int64(streams)
	e := b.ForEach(func(_, raw []byte) error {
		var previous telegramFreeRequest
		if e := json.Unmarshal(raw, &previous); e != nil {
			return e
		}
		if previous.AccountID != "" {
			if now.Before(previous.decisionTime().Add(freeCooldown)) {
				issued++
			}
			active, err := getClient(tx, previous.AccountID)
			if err == nil {
				owned++
				if active.ID != replacing && active.Status(now) == "active" {
					limit := active.StreamLimit
					if limit == 0 {
						limit = freeStreamBudget
					}
					totalStreams += int64(limit)
				}
			} else if !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	if replacing == "" && issued >= freeDailyLimit {
		return errFreeDailyLimit
	}
	if (replacing == "" && owned >= freeOwnedLimit) || totalStreams > freeStreamBudget {
		return errFreeCapacity
	}
	return nil
}

func (s *Store) saveFreeCustom(chatID int64, plan freePlan, expected string, revision uint64, now time.Time) error {
	if e := plan.validate(); e != nil {
		return e
	}
	chat := strconv.FormatInt(chatID, 10)
	key := []byte(chat)
	in := plan.account(chat, now)
	save := func(tx *bolt.Tx, account Client) error {
		catalog, e := readFreeCatalog(tx)
		if e != nil {
			return e
		}
		m, e := readFreeMember(tx.Bucket(meta).Bucket(freeMembers), key)
		if e != nil {
			return e
		}
		if catalog.RestoreHold || m.Blocked || !m.Started {
			return errors.New("user must first open the bot and have public access")
		}
		b, e := tx.Bucket(meta).CreateBucketIfNotExists(freeRequests)
		if e != nil {
			return e
		}
		var req telegramFreeRequest
		if raw := b.Get(key); raw != nil {
			if e = json.Unmarshal(raw, &req); e != nil {
				return e
			}
			if req.AccountID != expected && !expiredFreeRequest(tx, req, now) {
				return ErrConflict
			}
		}
		if expected != "" && req.AccountID != expected {
			return ErrConflict
		}
		if expected == "" {
			if e = checkFreeCapacity(tx, b, now, in.StreamLimit, ""); e != nil {
				return e
			}
			if req.At.IsZero() || expiredFreeRequest(tx, req, now) {
				req = telegramFreeRequest{Key: chat + ":" + token()[:12], At: now}
			}
			req.AccountID, req.DecidedAt = account.ID, now
		} else if e = checkFreeCapacity(tx, b, now, in.StreamLimit, expected); e != nil {
			return e
		}
		req.Plan, req.Reason, req.Reviewed, req.Cancelled = &plan, "", true, false
		return putJSON(b, key, req)
	}
	if expected == "" {
		_, e := s.create(in, false, save)
		return e
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		account, e := getClient(tx, expected)
		if e != nil {
			return e
		}
		if account.Revision != revision {
			return ErrConflict
		}
		if e = save(tx, account); e != nil {
			return e
		}
		applyInput(&account, in)
		account.Revision++
		account.UpdatedAt = now.UnixMilli()
		if e = putJSON(tx.Bucket(clients), []byte(expected), account); e != nil {
			return e
		}
		return addEvent(tx, "client.updated", expected)
	})
}

func (s *Store) linkFreeAccount(chatID int64, accountID string, revision uint64, now time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		account, e := getClient(tx, accountID)
		if e != nil {
			return e
		}
		if account.Revision != revision {
			return ErrConflict
		}
		if account.InstallerProbe || account.Status(now) != "active" || account.ExpiresAt == 0 ||
			account.QuotaBytes == 0 || account.RateBytes == 0 || account.StreamLimit < 1 || account.StreamLimit > 128 {
			return errors.New("choose an active account with finite expiry, quota, speed and stream limits")
		}
		catalog, e := readFreeCatalog(tx)
		if e != nil {
			return e
		}
		chat := strconv.FormatInt(chatID, 10)
		member, e := readFreeMember(tx.Bucket(meta).Bucket(freeMembers), []byte(chat))
		if e != nil {
			return e
		}
		if catalog.RestoreHold || !member.Started || member.Blocked {
			return errFreePaused
		}
		b, e := tx.Bucket(meta).CreateBucketIfNotExists(freeRequests)
		if e != nil {
			return e
		}
		if e = b.ForEach(func(k, raw []byte) error {
			var req telegramFreeRequest
			if e := json.Unmarshal(raw, &req); e != nil {
				return e
			}
			if req.AccountID == accountID || (string(k) == chat && req.AccountID != "" && !expiredFreeRequest(tx, req, now)) {
				return ErrConflict
			}
			return nil
		}); e != nil {
			return e
		}
		if e = checkFreeCapacity(tx, b, now, account.StreamLimit, ""); e != nil {
			return e
		}
		return putJSON(b, []byte(chat), telegramFreeRequest{Key: chat + ":" + token()[:12], AccountID: accountID,
			At: now, DecidedAt: now, Reviewed: true, Linked: true})
	})
}

func (s *Store) pruneFreeRequests(now time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(meta).Bucket(freeRequests)
		if b == nil {
			return nil
		}
		cursor := b.Cursor()
		for key, raw := cursor.First(); key != nil; key, raw = cursor.Next() {
			var req telegramFreeRequest
			if e := json.Unmarshal(raw, &req); e != nil {
				return e
			}
			if expiredFreeRequest(tx, req, now) {
				if e := cursor.Delete(); e != nil {
					return e
				}
			}
		}
		return nil
	})
}

func inspectFreeRequests(tx *bolt.Tx) error {
	c, e := readFreeCatalog(tx)
	if e != nil {
		return e
	}
	if e = c.validate(); e != nil {
		return e
	}
	if e = inspectFreeMembers(tx); e != nil {
		return e
	}
	if e = inspectFreeBroadcast(tx); e != nil {
		return e
	}
	b := tx.Bucket(meta).Bucket(freeRequests)
	if b == nil {
		return nil
	}
	owners := make(map[string]bool)
	return b.ForEach(func(k, raw []byte) error {
		const invalid = "invalid Telegram free configuration record"
		var req telegramFreeRequest
		if !validFreeChatID(string(k)) || len(raw) > 8192 ||
			json.Unmarshal(raw, &req) != nil || req.At.IsZero() {
			return errors.New(invalid)
		}
		chat, nonce, _ := strings.Cut(req.Key, ":")
		if _, e := base64.RawURLEncoding.DecodeString(nonce); chat != string(k) || len(nonce) != 12 || e != nil {
			return errors.New(invalid)
		}
		if req.Reviewed && req.Reason != "" || !req.Reviewed && (!validFreeReason(req.Reason) || req.AccountID != "") {
			return errors.New(invalid)
		}
		if req.Plan != nil && req.Plan.validate() != nil {
			return errors.New(invalid)
		}
		if req.AccountID != "" {
			if !identityPattern.MatchString(req.AccountID) || owners[req.AccountID] {
				return errors.New(invalid)
			}
			owners[req.AccountID] = true
		}
		return nil
	})
}
