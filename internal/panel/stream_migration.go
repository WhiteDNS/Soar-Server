package panel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

type StreamMigrationResult struct {
	Accounts int  `json:"accounts"`
	Restored bool `json:"restored"`
}

// MigrateStreamLimits is an offline operation: bbolt's exclusive file lock
// refuses a database held by the running panel. It changes only stream-limit
// JSON fields, preserving even fields unknown to this version of the panel.
// Restore mode takes the original limits from a stopped-service backup while
// retaining live usage and all other data. Unmatched accounts are untouched
// unless an explicit fallback is requested for compatibility with an old binary.
func MigrateStreamLimits(path, restoreFrom string, unmatchedLimit *int) (result StreamMigrationResult, err error) {
	if unmatchedLimit != nil && (restoreFrom == "" || *unmatchedLimit < 1 || *unmatchedLimit > MaxStreamLimit) {
		return result, errors.New("restore-unmatched-limit requires restore-from and a limit between 1 and 2147483647")
	}
	var restoredSettings map[string]json.RawMessage
	restoredAccounts := map[string]json.RawMessage{}
	if restoreFrom != "" {
		livePath, e := filepath.Abs(path)
		if e != nil {
			return result, e
		}
		sourcePath, e := filepath.Abs(restoreFrom)
		if e != nil {
			return result, e
		}
		if livePath == sourcePath {
			return result, errors.New("restore source must differ from the live database")
		}
		source, e := openStreamMigrationDB(restoreFrom, true)
		if e != nil {
			return result, e
		}
		e = source.View(func(tx *bolt.Tx) error {
			if e := inspectBackupTransaction(tx, &BackupSummary{}); e != nil {
				return e
			}
			if e := json.Unmarshal(tx.Bucket(meta).Get([]byte("settings")), &restoredSettings); e != nil {
				return e
			}
			return tx.Bucket(clients).ForEach(func(key, raw []byte) error {
				var fields map[string]json.RawMessage
				if e := json.Unmarshal(raw, &fields); e != nil {
					return e
				}
				restoredAccounts[string(key)] = bytes.Clone(fields["stream_limit"])
				return nil
			})
		})
		closeErr := source.Close()
		if e != nil {
			return result, fmt.Errorf("invalid restore source: %w", e)
		}
		if closeErr != nil {
			return result, closeErr
		}
		result.Restored = true
	}
	db, err := openStreamMigrationDB(path, false)
	if err != nil {
		return result, err
	}
	defer db.Close()
	// Validate committed pages before opening a writable transaction. bbolt's
	// integrity walk is intended for read transactions, not dirty pages.
	if err = db.View(func(tx *bolt.Tx) error { return inspectBackupTransaction(tx, &BackupSummary{}) }); err != nil {
		return result, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		var cfg map[string]json.RawMessage
		if e := json.Unmarshal(tx.Bucket(meta).Get([]byte("settings")), &cfg); e != nil {
			return e
		}
		if result.Restored {
			var before, after Settings
			if e := json.Unmarshal(tx.Bucket(meta).Get([]byte("settings")), &before); e != nil {
				return e
			}
			raw, e := json.Marshal(restoredSettings)
			if e != nil {
				return e
			}
			if e = json.Unmarshal(raw, &after); e != nil || before.BaseZone != after.BaseZone {
				return errors.New("restore source belongs to another tunnel zone")
			}
			setRawLimit(cfg, "server_stream_limit", restoredSettings["server_stream_limit"])
		} else {
			cfg["server_stream_limit"] = json.RawMessage("0")
		}
		if e := putJSON(tx.Bucket(meta), []byte("settings"), cfg); e != nil {
			return e
		}
		// Collect rows first: rewriting a bucket while walking its cursor may
		// otherwise skip records when a page splits.
		updates := map[string][]byte{}
		if e := tx.Bucket(clients).ForEach(func(key, raw []byte) error {
			var fields map[string]json.RawMessage
			if e := json.Unmarshal(raw, &fields); e != nil {
				return e
			}
			if result.Restored {
				if limit, ok := restoredAccounts[string(key)]; ok {
					setRawLimit(fields, "stream_limit", limit)
				} else if unmatchedLimit != nil {
					fields["stream_limit"] = json.RawMessage(fmt.Sprint(*unmatchedLimit))
				} else {
					return nil
				}
			} else {
				fields["stream_limit"] = json.RawMessage("0")
			}
			value, e := json.Marshal(fields)
			if e != nil {
				return e
			}
			updates[string(key)] = value
			return nil
		}); e != nil {
			return e
		}
		for key, raw := range updates {
			if e := tx.Bucket(clients).Put([]byte(key), raw); e != nil {
				return e
			}
		}
		// Validate the final JSON documents before the atomic commit; all other
		// buckets remain untouched and were checked in the exclusive read above.
		var settings Settings
		if e := json.Unmarshal(tx.Bucket(meta).Get([]byte("settings")), &settings); e != nil {
			return e
		}
		if e := settings.Validate(); e != nil {
			return e
		}
		for key := range updates {
			c, e := getClient(tx, key)
			if e != nil {
				return e
			}
			if e = (ClientInput{Name: c.Name, Note: c.Note, Engines: c.Engines, Enabled: c.Enabled,
				QuotaBytes: c.QuotaBytes, ExpiresAt: c.ExpiresAt, RateBytes: c.RateBytes, StreamLimit: c.StreamLimit}).validate(); e != nil {
				return e
			}
		}
		result.Accounts = len(updates)
		return nil
	})
	return result, err
}

func setRawLimit(fields map[string]json.RawMessage, name string, raw json.RawMessage) {
	if raw == nil {
		delete(fields, name)
	} else {
		fields[name] = bytes.Clone(raw)
	}
}

func openStreamMigrationDB(path string, readOnly bool) (*bolt.DB, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("migration requires an existing regular database file")
	}
	db, err := bolt.Open(path, info.Mode().Perm(), &bolt.Options{ReadOnly: readOnly, Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("cannot lock database; stop the panel before migrating: %w", err)
	}
	return db, nil
}
