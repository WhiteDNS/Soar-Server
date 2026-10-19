package panel

import (
	"encoding/hex"
	"errors"
	"net/http"

	bolt "go.etcd.io/bbolt"
)

// DeleteMany validates the complete selection and removes it in one transaction.
// A stale or invalid ID must never leave only part of a selection deleted.
func (s *Store) DeleteMany(ids []string) error {
	if len(ids) == 0 || len(ids) > MaxClients {
		return errors.New("select between 1 and 512 accounts")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		_, err := hex.DecodeString(id)
		if len(id) != 32 || err != nil || seen[id] {
			return errors.New("invalid or duplicate account ID")
		}
		seen[id] = true
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, id := range ids {
			if _, err := getClient(tx, id); err != nil {
				return err
			}
		}
		for _, id := range ids {
			if err := deleteClient(tx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (h *HTTP) deleteClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var in struct {
		IDs []string `json:"ids"`
	}
	// 512 identifiers need about 18 KiB, exceeding the usual 16 KiB form limit.
	if err := decodeJSONLimit(w, r, &in, 32*1024); err != nil {
		errorReply(w, 400, err)
		return
	}
	if err := h.store.DeleteMany(in.IDs); err != nil {
		status := 400
		if errors.Is(err, ErrNotFound) {
			status = 404
		}
		errorReply(w, status, err)
		return
	}
	for _, id := range in.IDs {
		h.manager.Disconnect(id)
	}
	if err := h.manager.Reconcile(); err != nil {
		errorReply(w, 500, errors.New("accounts deleted; runtime synchronization failed"))
		return
	}
	jsonReply(w, 200, map[string]int{"deleted": len(in.IDs)})
}
