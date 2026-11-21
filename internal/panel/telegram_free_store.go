package panel

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	freeRequests          = []byte("telegram_free_requests")
	errFreeQueueFull      = errors.New("free configuration queue is full")
	errFreeAdmissionLimit = errors.New("24-hour free application limit reached")
	errFreeDailyLimit     = errors.New("daily free configuration limit reached")
	errFreePanelSlots     = fmt.Errorf("%w: reserved panel slots", errFreeCapacity)
	errFreeAccountSlots   = fmt.Errorf("%w: free account count", errFreeCapacity)
	errFreeStreamSlots    = fmt.Errorf("%w: active stream budget", errFreeCapacity)
	errFreeRetryRequired  = errors.New("the previous account was removed; open Manage a Telegram user to review the waiting period or allow a new request")
	errFreeExistingPlan   = errors.New("existing account settings are outside free plan limits; review the account's name, quota, speed and connection limits in the panel")
)

type telegramFreeRequest struct {
	Key             string
	Reason          string `json:",omitempty"`
	AccountID       string `json:",omitempty"`
	Reviewed        bool
	At              time.Time
	Plan            *freePlan   `json:",omitempty"`
	DecidedAt       time.Time   `json:",omitzero"`
	RemovedAt       time.Time   `json:",omitzero"`
	Cancelled       bool        `json:",omitempty"`
	Linked          bool        `json:",omitempty"`
	Notified        bool        `json:",omitempty"`
	NotifiedAdmins  []int64     `json:",omitempty"`
	RetryAllowed    bool        `json:",omitempty"`
	PriorAdmissions []time.Time `json:",omitempty"`
	PriorIssuances  []time.Time `json:",omitempty"`
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
		catalog, e := readFreeCatalog(tx)
		if e != nil {
			return e
		}
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
		if expiredFreeRequest(tx, req, now, catalog.limits()) {
			req = telegramFreeRequest{}
		}
		return nil
	})
	return
}

func expiredFreeRequest(tx *bolt.Tx, req telegramFreeRequest, now time.Time, limits freeLimits) bool {
	return !now.Before(req.retryAt(limits)) && !freeAccountExists(tx, req)
}

func (r telegramFreeRequest) retryAt(limits freeLimits) time.Time {
	if !r.Reviewed {
		return r.At.Add(time.Duration(limits.PendingHours) * time.Hour)
	}
	return maxTime(r.decisionTime(), r.RemovedAt).Add(time.Duration(limits.RetryHours) * time.Hour)
}

func (r telegramFreeRequest) hasRecentHistory(now time.Time) bool {
	return now.Before(maxTime(r.At, r.decisionTime()).Add(freeCooldown)) ||
		recentFreeEvents(r.PriorAdmissions, now) > 0 || recentFreeEvents(r.PriorIssuances, now) > 0
}

func freeAccountExists(tx *bolt.Tx, r telegramFreeRequest) bool {
	return r.AccountID != "" && tx.Bucket(clients).Get([]byte(r.AccountID)) != nil
}

func recentFreeEvents(times []time.Time, now time.Time) int {
	count := 0
	for _, at := range times {
		if now.Before(at.Add(freeCooldown)) {
			count++
		}
	}
	return count
}

func (r *telegramFreeRequest) inheritFreeEvents(previous telegramFreeRequest, now time.Time) {
	for _, at := range append(previous.PriorAdmissions, previous.At) {
		if !at.IsZero() && now.Before(at.Add(freeCooldown)) {
			r.PriorAdmissions = append(r.PriorAdmissions, at)
		}
	}
	issued := previous.PriorIssuances
	if previous.AccountID != "" {
		issued = append(issued, previous.decisionTime())
	}
	for _, at := range issued {
		if now.Before(at.Add(freeCooldown)) {
			r.PriorIssuances = append(r.PriorIssuances, at)
		}
	}
}

func (s *Store) setFreeRetry(chatID int64, expected string, allow bool, now time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		catalog, e := readFreeCatalog(tx)
		if e != nil {
			return e
		}
		b := tx.Bucket(meta).Bucket(freeRequests)
		key := []byte(strconv.FormatInt(chatID, 10))
		if b == nil || b.Get(key) == nil {
			return ErrConflict
		}
		var req telegramFreeRequest
		if e := json.Unmarshal(b.Get(key), &req); e != nil {
			return e
		}
		if req.Key != expected || !req.Reviewed || freeAccountExists(tx, req) || expiredFreeRequest(tx, req, now, catalog.limits()) || req.RetryAllowed == allow {
			return ErrConflict
		}
		req.RetryAllowed = allow
		if e := putJSON(b, key, req); e != nil {
			return e
		}
		action := "telegram.free_retry_revoked"
		if allow {
			action = "telegram.free_retry_allowed"
		}
		return addEvent(tx, action, string(key))
	})
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
		limits := catalog.limits()
		var outdated [][]byte
		pending, recent := 0, 0
		next := telegramFreeRequest{Key: chat + ":" + nonce, Reason: reason, At: now, Plan: &p}
		e = b.ForEach(func(k, raw []byte) error {
			var req telegramFreeRequest
			if e := json.Unmarshal(raw, &req); e != nil {
				return e
			}
			if now.Before(req.At.Add(freeCooldown)) {
				recent++
			}
			recent += recentFreeEvents(req.PriorAdmissions, now)
			switch {
			case expiredFreeRequest(tx, req, now, limits):
				if string(k) == chat {
					next.inheritFreeEvents(req, now)
				} else if !req.hasRecentHistory(now) {
					outdated = append(outdated, append([]byte(nil), k...))
				}
			case string(k) == chat:
				if !req.RetryAllowed || !req.Reviewed || freeAccountExists(tx, req) {
					return ErrConflict
				}
				next.inheritFreeEvents(req, now)
			case !req.Reviewed:
				pending++
			}
			return nil
		})
		if e != nil {
			return e
		}
		if pending >= limits.Pending {
			return errFreeQueueFull
		}
		if recent >= limits.Admissions {
			return errFreeAdmissionLimit
		}
		for _, key := range outdated {
			if e := b.Delete(key); e != nil {
				return e
			}
		}
		return putJSON(b, []byte(chat), next)
	})
}

func (s *Store) freePending(now time.Time) (pending []telegramFreeRequest, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		catalog, e := readFreeCatalog(tx)
		if e != nil {
			return e
		}
		b := tx.Bucket(meta).Bucket(freeRequests)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var req telegramFreeRequest
			if e := json.Unmarshal(raw, &req); e != nil {
				return e
			}
			if !req.Reviewed && !expiredFreeRequest(tx, req, now, catalog.limits()) {
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
	return s.issueFree(key, now, false)
}

func (s *Store) issueFree(key string, now time.Time, automatic bool) (Client, error) {
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
		return reviewFree(tx, key, account.ID, now, automatic)
	})
}

func (s *Store) cancelFree(id int64, now time.Time) (bool, error) {
	cancelled := false
	err := s.db.Update(func(tx *bolt.Tx) error {
		catalog, e := readFreeCatalog(tx)
		if e != nil {
			return e
		}
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
		if req.Reviewed || expiredFreeRequest(tx, req, now, catalog.limits()) {
			return nil
		}
		req.Reviewed, req.Cancelled, req.DecidedAt, req.Reason = true, true, now, ""
		cancelled = true
		return putJSON(b, key, req)
	})
	return cancelled, err
}

func reviewFree(tx *bolt.Tx, key, accountID string, now time.Time, automatic bool) error {
	chatID, _, _ := strings.Cut(key, ":")
	b := tx.Bucket(meta).Bucket(freeRequests)
	if b == nil || b.Get([]byte(chatID)) == nil {
		return ErrNotFound
	}
	var req telegramFreeRequest
	if e := json.Unmarshal(b.Get([]byte(chatID)), &req); e != nil {
		return e
	}
	catalog, err := readFreeCatalog(tx)
	if err != nil {
		return err
	}
	if req.Key != key || req.Reviewed || expiredFreeRequest(tx, req, now, catalog.limits()) {
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
		if catalog.RestoreHold || automatic && catalog.Paused {
			return errFreePaused
		}
		// Eligibility uses the current policy; approved limits come from the saved request.
		current, ok := catalog.plan(req.plan().ID)
		snapshot := req.plan()
		snapshot.Published = current.Published
		if !ok || !catalog.offers(current) || !catalog.offers(snapshot) {
			return errFreePlanUnavailable
		}
		if automatic && (!current.AutoApprove || !snapshot.AutoApprove || current.Revision != snapshot.Revision) {
			return errFreePlanUnavailable
		}
		if e = checkFreeCapacity(tx, b, now, snapshot.Input.StreamLimit, "", catalog.limits()); e != nil {
			return e
		}
	}
	req.Reviewed, req.DecidedAt, req.Reason, req.AccountID = true, now, "", accountID
	if err := putJSON(b, []byte(chatID), req); err != nil {
		return err
	}
	if automatic {
		return addEvent(tx, "telegram.free_auto_approved", accountID)
	}
	return nil
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

func freeAccountStreams(account Client) int {
	if account.StreamLimit == 0 {
		return freeStreamBudget
	}
	return account.StreamLimit
}

func checkFreeCapacity(tx *bolt.Tx, b *bolt.Bucket, now time.Time, streams int, replacing string, limits freeLimits) error {
	accounts := tx.Bucket(clients)
	if replacing == "" && accounts.Stats().KeyN >= MaxClients-limits.Headroom {
		return errFreePanelSlots
	}
	issued, owned, totalStreams := 0, 0, int64(streams)
	e := b.ForEach(func(_, raw []byte) error {
		var previous telegramFreeRequest
		if e := json.Unmarshal(raw, &previous); e != nil {
			return e
		}
		issued += recentFreeEvents(previous.PriorIssuances, now)
		if previous.AccountID != "" {
			if now.Before(previous.decisionTime().Add(freeCooldown)) {
				issued++
			}
			active, err := getClient(tx, previous.AccountID)
			if err == nil {
				owned++
				if active.ID != replacing && active.Status(now) == "active" {
					totalStreams += int64(freeAccountStreams(active))
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
	if replacing == "" && issued >= limits.Issuance {
		return errFreeDailyLimit
	}
	if replacing == "" && owned >= limits.Accounts {
		return errFreeAccountSlots
	}
	if totalStreams > int64(limits.Streams) {
		return errFreeStreamSlots
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
			if req.AccountID != expected && !expiredFreeRequest(tx, req, now, catalog.limits()) &&
				(!req.RetryAllowed || !req.Reviewed || freeAccountExists(tx, req)) {
				return ErrConflict
			}
		}
		if expected != "" && req.AccountID != expected {
			return ErrConflict
		}
		if expected == "" {
			if e = checkFreeCapacity(tx, b, now, in.StreamLimit, "", catalog.limits()); e != nil {
				return e
			}
			if req.At.IsZero() || req.RetryAllowed || expiredFreeRequest(tx, req, now, catalog.limits()) {
				previous := req
				req = telegramFreeRequest{Key: chat + ":" + token()[:12], At: now}
				req.inheritFreeEvents(previous, now)
			}
			req.AccountID, req.DecidedAt = account.ID, now
		} else if e = checkFreeCapacity(tx, b, now, in.StreamLimit, expected, catalog.limits()); e != nil {
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
		next := telegramFreeRequest{Key: chat + ":" + token()[:12], AccountID: accountID,
			At: now, DecidedAt: now, Reviewed: true, Linked: true}
		if e = b.ForEach(func(k, raw []byte) error {
			var req telegramFreeRequest
			if e := json.Unmarshal(raw, &req); e != nil {
				return e
			}
			if req.AccountID == accountID {
				return ErrConflict
			}
			if string(k) == chat {
				if req.AccountID != "" && !expiredFreeRequest(tx, req, now, catalog.limits()) && (!req.RetryAllowed || freeAccountExists(tx, req)) {
					return ErrConflict
				}
				next.inheritFreeEvents(req, now)
			}
			return nil
		}); e != nil {
			return e
		}
		if e = checkFreeCapacity(tx, b, now, account.StreamLimit, "", catalog.limits()); e != nil {
			return e
		}
		return putJSON(b, []byte(chat), next)
	})
}

func (s *Store) pruneFreeRequests(now time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		catalog, e := readFreeCatalog(tx)
		if e != nil {
			return e
		}
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
			if expiredFreeRequest(tx, req, now, catalog.limits()) && !req.hasRecentHistory(now) {
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
		if !validFreeChatID(string(k)) || len(raw) > 128<<10 ||
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
		if req.RetryAllowed && (!req.Reviewed || freeAccountExists(tx, req)) ||
			len(req.PriorAdmissions) > maxFreeAdmissions+maxFreeIssuance || len(req.PriorIssuances) > maxFreeIssuance ||
			len(req.NotifiedAdmins) > telegramMaxAdmins {
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
