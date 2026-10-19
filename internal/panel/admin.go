package panel

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

var errAdminCredentials = errors.New("current administrator credentials are incorrect")
var errAdminChanged = errors.New("administrator credentials changed; sign in again")

type adminChange struct {
	CurrentName     string `json:"current_name"`
	CurrentPassword string `json:"current_password"`
	NewName         string `json:"new_name"`
	NewPassword     string `json:"new_password"`
}

func (s *Store) admin() (a Admin, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		return json.Unmarshal(tx.Bucket(meta).Get([]byte("admin")), &a)
	})
	return
}

func (s *Store) changeAdmin(in adminChange) error {
	if len(in.NewName) < 1 || len(in.NewName) > 64 {
		return errors.New("administrator name must be 1-64 letters, numbers, dots, underscores or hyphens")
	}
	for _, c := range in.NewName {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return errors.New("administrator name must be 1-64 letters, numbers, dots, underscores or hyphens")
		}
	}
	if in.NewPassword != "" {
		if len(in.NewPassword) < 12 || len(in.NewPassword) > 72 {
			return errors.New("new password must be 12-72 printable ASCII characters")
		}
		for _, c := range in.NewPassword {
			if c < 32 || c > 126 {
				return errors.New("new password must be 12-72 printable ASCII characters")
			}
		}
	}
	old, err := s.admin()
	if err != nil {
		return err
	}
	if len(in.CurrentPassword) > 72 || bcrypt.CompareHashAndPassword(old.Hash, []byte(in.CurrentPassword)) != nil || old.Name != in.CurrentName {
		return errAdminCredentials
	}
	next := Admin{Name: in.NewName, Hash: old.Hash}
	if in.NewPassword != "" {
		next.Hash, err = bcrypt.GenerateFromPassword([]byte(in.NewPassword), 12)
		if err != nil {
			return err
		}
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		var current Admin
		b := tx.Bucket(meta)
		if err := json.Unmarshal(b.Get([]byte("admin")), &current); err != nil {
			return err
		}
		// Do not overwrite a credential change that happened during password hashing.
		if current.Name != old.Name || !bytes.Equal(current.Hash, old.Hash) {
			return errAdminChanged
		}
		if err := putJSON(b, []byte("admin"), next); err != nil {
			return err
		}
		return addEvent(tx, "admin.credentials_updated", "")
	})
}

func (h *HTTP) adminCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		a, err := h.store.admin()
		if err != nil {
			errorReply(w, 500, errors.New("cannot read administrator account"))
			return
		}
		jsonReply(w, 200, map[string]string{"name": a.Name})
		return
	}
	if r.Method != "PUT" {
		w.WriteHeader(405)
		return
	}
	var in adminChange
	if err := decodeJSON(w, r, &in); err != nil {
		errorReply(w, 400, err)
		return
	}
	ip, allowed := h.authAttempt(w, r)
	if !allowed {
		return
	}
	h.authMu.Lock()
	defer h.authMu.Unlock()
	// A waiting request may have authenticated before another change revoked it.
	if _, ok := h.authenticated(r); !ok {
		errorReply(w, 401, errors.New("sign in required"))
		return
	}
	if err := h.store.changeAdmin(in); err != nil {
		status := 400
		if errors.Is(err, errAdminCredentials) {
			status = 403
		} else if errors.Is(err, errAdminChanged) {
			status = 409
		}
		errorReply(w, status, err)
		return
	}
	h.mu.Lock()
	h.sessions = map[string]loginSession{}
	delete(h.attempts, ip)
	h.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "soar_panel_session", Value: "", Path: h.cookiePath(), HttpOnly: true, Secure: h.isSecure(r), SameSite: http.SameSiteStrictMode, MaxAge: -1})
	jsonReply(w, 200, map[string]bool{"ok": true})
}
