package panel

import (
	"errors"
	"net/http"
)

type exportedAccount struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Links        map[string]string `json:"links"`
	Subscription string            `json:"subscription"`
}

// exportClients returns the profile links and subscription URL of every
// selected account. POST keeps the request behind the CSRF check and the
// identifiers out of URLs; the reply carries account keys, so it is audited.
func (h *HTTP) exportClients(w http.ResponseWriter, r *http.Request) {
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
	if len(in.IDs) == 0 || len(in.IDs) > MaxClients {
		errorReply(w, 400, errors.New("select between 1 and 512 accounts"))
		return
	}
	out := make([]exportedAccount, 0, len(in.IDs))
	for _, id := range in.IDs {
		c, err := h.store.Get(id)
		if err != nil {
			errorReply(w, 404, ErrNotFound)
			return
		}
		links, err := h.store.Profiles(id)
		if err != nil {
			errorReply(w, 500, errors.New("cannot render profile"))
			return
		}
		credential, err := h.store.SubscriptionToken(id, false)
		if err != nil {
			errorReply(w, 500, errors.New("cannot prepare subscription"))
			return
		}
		out = append(out, exportedAccount{ID: id, Name: c.Name, Links: links, Subscription: h.subscriptionURL(r, credential)})
	}
	_ = h.store.Audit("clients.exported", "")
	w.Header().Set("Cache-Control", "no-store")
	jsonReply(w, 200, map[string]any{"accounts": out})
}
