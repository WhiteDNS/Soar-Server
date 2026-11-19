package panel

import (
	"errors"
	"net/http"
	"slices"
)

type exportedAccount struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Domains      []DomainProfiles `json:"domains"`
	Subscription string           `json:"subscription"`
}

// exportClients returns the profile links on the chosen tunnel domains (all of
// them by default, preferred first) and the subscription URL of every selected
// account. POST keeps the request behind the CSRF check and the
// identifiers out of URLs; the reply carries account keys, so it is audited.
func (h *HTTP) exportClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var in struct {
		IDs   []string `json:"ids"`
		Zones []string `json:"zones"`
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
	cfg, err := h.store.Settings()
	if err != nil {
		errorReply(w, 500, errors.New("cannot read settings"))
		return
	}
	zones := cfg.orderedZones()
	if len(in.Zones) > 0 {
		known, want := map[string]bool{}, map[string]bool{}
		for _, z := range zones {
			known[z] = true
		}
		for _, z := range in.Zones {
			if !known[z] || want[z] {
				errorReply(w, 400, errors.New("unknown or duplicate tunnel domain"))
				return
			}
			want[z] = true
		}
		zones = slices.DeleteFunc(zones, func(z string) bool { return !want[z] })
	}
	out := make([]exportedAccount, 0, len(in.IDs))
	for _, id := range in.IDs {
		c, err := h.store.Get(id)
		if err != nil {
			errorReply(w, 404, ErrNotFound)
			return
		}
		domains, err := h.store.domainProfiles(c, cfg, zones)
		if err != nil {
			errorReply(w, 500, errors.New("cannot render profile"))
			return
		}
		credential, err := h.store.SubscriptionToken(id, false)
		if err != nil {
			errorReply(w, 500, errors.New("cannot prepare subscription"))
			return
		}
		out = append(out, exportedAccount{ID: id, Name: c.Name, Domains: domains, Subscription: h.subscriptionURL(r, credential)})
	}
	_ = h.store.Audit("clients.exported", "")
	w.Header().Set("Cache-Control", "no-store")
	jsonReply(w, 200, map[string]any{"accounts": out})
}
