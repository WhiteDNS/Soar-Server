package panel

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

const MaxBackupBytes = 32 << 20

var identityPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type BackupSummary struct {
	BaseZone string `json:"base_zone"`
	Accounts int    `json:"accounts"`
	SHA256   string `json:"sha256"`
	Bytes    int64  `json:"bytes"`
}

// InspectBackupFile is also exposed through an isolated, time-limited CLI child.
// Never initialize an uploaded database: missing buckets must be rejected.
func InspectBackupFile(path string) (summary BackupSummary, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("invalid backup database")
		}
	}()
	info, err := os.Stat(path)
	if err != nil {
		return summary, err
	}
	if !info.Mode().IsRegular() || info.Size() < 8192 || info.Size() > MaxBackupBytes {
		return summary, errors.New("backup must be a panel .db file, at most 32 MiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return summary, err
	}
	sum := sha256.Sum256(raw)
	summary.SHA256 = hex.EncodeToString(sum[:])
	summary.Bytes = info.Size()
	db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return summary, errors.New("invalid backup database")
	}
	defer db.Close()
	err = db.View(func(tx *bolt.Tx) error {
		for e := range tx.Check() {
			if e != nil {
				return errors.New("backup database integrity check failed")
			}
		}
		for _, name := range [][]byte{meta, clients, seeds, events} {
			if tx.Bucket(name) == nil {
				return errors.New("backup is missing panel tables")
			}
		}
		if err := tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			if !bytes.Equal(name, meta) && !bytes.Equal(name, clients) && !bytes.Equal(name, seeds) && !bytes.Equal(name, events) && !bytes.Equal(name, subscriptions) && !bytes.Equal(name, subscriptionIndex) {
				return errors.New("unsupported backup database version")
			}
			return nil
		}); err != nil {
			return err
		}
		var cfg Settings
		if err := json.Unmarshal(tx.Bucket(meta).Get([]byte("settings")), &cfg); err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		summary.BaseZone = cfg.BaseZone
		var admin Admin
		if err := json.Unmarshal(tx.Bucket(meta).Get([]byte("admin")), &admin); err != nil {
			return err
		}
		cost, e := bcrypt.Cost(admin.Hash)
		if e != nil || cost < 10 || cost > 14 || admin.Name == "" || len(admin.Name) > 64 {
			return errors.New("invalid backup administrator")
		}
		zones := map[string]bool{}
		if err := tx.Bucket(clients).ForEach(func(k, v []byte) error {
			if v == nil || len(v) > 8192 || !identityPattern.Match(k) {
				return errors.New("invalid backup account record")
			}
			var c Client
			if e := json.Unmarshal(v, &c); e != nil {
				return e
			}
			if c.ID != string(k) || c.Revision == 0 || c.UsageEpoch == 0 || c.UploadBytes < 0 || c.DownloadBytes < 0 || c.UploadBytes > 1<<60 || c.DownloadBytes > 1<<60 {
				return errors.New("invalid backup account counters")
			}
			in := ClientInput{Engines: c.Engines, Name: c.Name, Note: c.Note, Enabled: c.Enabled, QuotaBytes: c.QuotaBytes, ExpiresAt: c.ExpiresAt, RateBytes: c.RateBytes, StreamLimit: c.StreamLimit}
			if e := in.validate(); e != nil {
				return e
			}
			label := strings.TrimSuffix(c.Zone, "."+cfg.BaseZone)
			if label == c.Zone || len(label) < 1 || len(label) > 63 || strings.Contains(label, ".") || zones[c.Zone] {
				return errors.New("invalid or duplicate backup account zone")
			}
			for _, r := range label {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
					return errors.New("invalid backup account zone")
				}
			}
			zones[c.Zone] = true
			seed := tx.Bucket(seeds).Get(k)
			if len(seed) != ed25519.SeedSize || base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)) != c.PublicKey {
				return errors.New("backup account key mismatch")
			}
			summary.Accounts++
			if summary.Accounts > MaxClients {
				return errors.New("backup has too many accounts")
			}
			return nil
		}); err != nil {
			return err
		}
		if tx.Bucket(seeds).Stats().KeyN != summary.Accounts {
			return errors.New("backup key table mismatch")
		}
		// Older backups legitimately omit subscription tables. New backups must
		// contain a complete, account-bound token/index pair.
		subs, index := tx.Bucket(subscriptions), tx.Bucket(subscriptionIndex)
		if (subs == nil) != (index == nil) {
			return errors.New("backup subscription table mismatch")
		}
		if subs != nil {
			if subs.Stats().KeyN > summary.Accounts || index.Stats().KeyN != subs.Stats().KeyN {
				return errors.New("backup subscription index mismatch")
			}
			if err := subs.ForEach(func(k, v []byte) error {
				if !identityPattern.Match(k) || tx.Bucket(clients).Get(k) == nil || len(v) != 32 || !bytes.Equal(index.Get(subscriptionDigest(v)), k) {
					return errors.New("invalid backup subscription")
				}
				return nil
			}); err != nil {
				return err
			}
		}
		if tx.Bucket(events).Stats().KeyN > 2000 {
			return errors.New("backup has too many audit events")
		}
		return tx.Bucket(events).ForEach(func(k, v []byte) error {
			if v == nil || len(k) > 32 || len(v) > 4096 {
				return errors.New("invalid backup audit record")
			}
			var ev Event
			return json.Unmarshal(v, &ev)
		})
	})
	return summary, err
}

// Keep current administrator credentials when applying a full account restore.
// Access URLs and TLS certificates are machine-local and live outside the DB.
func PreserveRestoreAdmin(candidate, current string) error {
	old, e := bolt.Open(current, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if e != nil {
		return e
	}
	var admin []byte
	e = old.View(func(tx *bolt.Tx) error { admin = bytes.Clone(tx.Bucket(meta).Get([]byte("admin"))); return nil })
	old.Close()
	if e != nil {
		return e
	}
	db, e := bolt.Open(candidate, 0600, &bolt.Options{Timeout: time.Second})
	if e != nil {
		return e
	}
	defer db.Close()
	return db.Update(func(tx *bolt.Tx) error {
		if e := tx.Bucket(meta).Put([]byte("admin"), admin); e != nil {
			return e
		}
		return addEvent(tx, "backup.restored", "")
	})
}
func (s *Store) VerifyPassword(password string) bool {
	if len(password) > 72 {
		return false
	}
	var a Admin
	if e := s.db.View(func(tx *bolt.Tx) error { return json.Unmarshal(tx.Bucket(meta).Get([]byte("admin")), &a) }); e != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword(a.Hash, []byte(password)) == nil
}

func BackupMismatch(got, want string) error {
	return fmt.Errorf("backup zone %s does not match this server's zone %s", got, want)
}
