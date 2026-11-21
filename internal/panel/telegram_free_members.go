package panel

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	bolt "go.etcd.io/bbolt"
)

var freeMembers = []byte("telegram_free_members")

const freeMemberLimit = 4096

type freeMember struct {
	Language string
	Seen     time.Time
	Started  bool
	OptOut   bool
	Blocked  bool
}

func readFreeMember(b *bolt.Bucket, key []byte) (freeMember, error) {
	var m freeMember
	if b == nil || b.Get(key) == nil {
		return m, nil
	}
	e := json.Unmarshal(b.Get(key), &m)
	return m, e
}

func (s *Store) freeMember(id int64) (m freeMember, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		m, err = readFreeMember(tx.Bucket(meta).Bucket(freeMembers), []byte(strconv.FormatInt(id, 10)))
		return err
	})
	return
}

func (s *Store) updateFreeMember(id int64, now time.Time, change func(*freeMember)) error {
	if id < 1 {
		return errors.New("invalid Telegram ID")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b, e := tx.Bucket(meta).CreateBucketIfNotExists(freeMembers)
		if e != nil {
			return e
		}
		key := []byte(strconv.FormatInt(id, 10))
		m, e := readFreeMember(b, key)
		if e != nil {
			return e
		}
		if b.Get(key) == nil && b.Stats().KeyN >= freeMemberLimit {
			cursor := b.Cursor()
			var oldestKey []byte
			var oldest time.Time
			for k, raw := cursor.First(); k != nil; k, raw = cursor.Next() {
				var candidate freeMember
				if e := json.Unmarshal(raw, &candidate); e != nil {
					return e
				}
				if candidate.Blocked || candidate.OptOut || (oldestKey != nil && !candidate.Seen.Before(oldest)) {
					continue
				}
				requests := tx.Bucket(meta).Bucket(freeRequests)
				if requests != nil && requests.Get(k) != nil {
					continue
				}
				oldestKey, oldest = append([]byte(nil), k...), candidate.Seen
			}
			if oldestKey == nil {
				return errors.New("Telegram audience capacity reached")
			}
			if e = b.Delete(oldestKey); e != nil {
				return e
			}
		}
		m.Seen = now
		change(&m)
		return putJSON(b, key, m)
	})
}

func blockedFreeMember(tx *bolt.Tx, key []byte) (bool, error) {
	m, e := readFreeMember(tx.Bucket(meta).Bucket(freeMembers), key)
	return m.Blocked, e
}

func (s *Store) hasFreeOwner(id int64) bool {
	owned := false
	_ = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(meta).Bucket(freeRequests)
		if b == nil {
			return nil
		}
		var r telegramFreeRequest
		raw := b.Get([]byte(strconv.FormatInt(id, 10)))
		owned = raw != nil && json.Unmarshal(raw, &r) == nil && r.AccountID != "" &&
			tx.Bucket(clients).Get([]byte(r.AccountID)) != nil
		return nil
	})
	return owned
}

func validFreeChatID(raw string) bool {
	id, e := strconv.ParseInt(raw, 10, 64)
	return e == nil && id > 0 && id < 1<<52 && strconv.FormatInt(id, 10) == raw
}

func inspectFreeMembers(tx *bolt.Tx) error {
	b := tx.Bucket(meta).Bucket(freeMembers)
	if b == nil {
		return nil
	}
	if b.Stats().KeyN > freeMemberLimit {
		return errors.New("too many Telegram audience records")
	}
	return b.ForEach(func(k, raw []byte) error {
		var m freeMember
		if len(raw) > 2048 || !validFreeChatID(string(k)) || json.Unmarshal(raw, &m) != nil ||
			!oneOf(m.Language, "", "fa", "en") || m.Seen.IsZero() {
			return errors.New("invalid Telegram audience record")
		}
		return nil
	})
}
