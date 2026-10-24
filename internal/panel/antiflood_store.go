package panel

import (
	"encoding/json"
	"errors"
	"time"

	bolt "go.etcd.io/bbolt"
)

var floodBlockKey = []byte("antiflood_blocks")

func readFloodBlocks(b *bolt.Bucket) (map[string]FloodBlock, error) {
	blocks := map[string]FloodBlock{}
	if raw := b.Get(floodBlockKey); raw != nil {
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, errors.New("invalid antiflood quarantine data")
		}
	}
	if blocks == nil || len(blocks) > MaxClients+1 {
		return nil, errors.New("invalid antiflood quarantine data")
	}
	for id, v := range blocks {
		if !identityPattern.MatchString(id) || v.At < 0 || v.Until < v.At || v.Until-v.At > 3600000 || v.Strikes < 1 || v.Strikes > 5 || !validFloodBlockReason(v.Reason) {
			return nil, errors.New("invalid antiflood quarantine record")
		}
	}
	return blocks, nil
}

func validFloodBlockReason(reason string) bool {
	switch reason {
	case "account_rate", "target_scan", "dial_failures", "resolver_rate":
		return true
	}
	return false
}

func (s *Store) floodBlocks() (blocks map[string]FloodBlock, err error) {
	err = s.db.View(func(tx *bolt.Tx) error { blocks, err = readFloodBlocks(tx.Bucket(meta)); return err })
	return
}

func (s *Store) saveFloodBlock(id string, block *FloodBlock) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket(clients).Get([]byte(id)) == nil {
			return ErrNotFound
		}
		blocks, err := readFloodBlocks(tx.Bucket(meta))
		if err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		for key, v := range blocks {
			if now-v.At > 86400000 || tx.Bucket(clients).Get([]byte(key)) == nil {
				delete(blocks, key)
			}
		}
		action := "antiflood.released"
		if block == nil {
			delete(blocks, id)
		} else {
			blocks[id] = *block
			action = "antiflood.quarantined." + block.Reason
		}
		if err := putJSON(tx.Bucket(meta), floodBlockKey, blocks); err != nil {
			return err
		}
		return addEvent(tx, action, id)
	})
}

func (s *Store) saveAntiFloodPolicy(policy AntiFloodPolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(meta)
		var cfg Settings
		if err := json.Unmarshal(b.Get([]byte("settings")), &cfg); err != nil {
			return err
		}
		cfg.AntiFlood = &policy
		if err := cfg.Validate(); err != nil {
			return err
		}
		if err := putJSON(b, []byte("settings"), cfg); err != nil {
			return err
		}
		return addEvent(tx, "antiflood.policy_updated", "")
	})
}
