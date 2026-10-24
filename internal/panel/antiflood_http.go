package panel

import (
	"errors"
	"net/http"
)

// Called only after authentication and the shared CSRF/origin check.
func (h *HTTP) antiFloodAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/antiflood/release" {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		var in struct {
			ClientID string `json:"client_id"`
		}
		if err := decodeJSON(w, r, &in); err != nil {
			errorReply(w, 400, err)
			return
		}
		if _, err := h.store.Get(in.ClientID); err != nil {
			errorReply(w, 404, ErrNotFound)
			return
		}
		if err := h.manager.flood.reset(in.ClientID); err != nil {
			errorReply(w, 500, errors.New("cannot release antiflood quarantine"))
			return
		}
	} else if r.Method == "PUT" {
		var p AntiFloodPolicy
		if err := decodeJSON(w, r, &p); err != nil {
			errorReply(w, 400, err)
			return
		}
		if err := h.store.saveAntiFloodPolicy(p); err != nil {
			errorReply(w, 400, err)
			return
		}
	} else if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	if err := h.manager.Reconcile(); err != nil {
		errorReply(w, 500, errors.New("cannot synchronize antiflood policy"))
		return
	}
	jsonReply(w, 200, h.manager.flood.snapshot())
}
