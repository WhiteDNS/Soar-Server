package panel

import "errors"

var errAccountRuntimeSync = errors.New("saved change; runtime synchronization failed")

// applyAccountAction keeps both administrator transports on the same runtime path.
// A revision, when supplied by a Telegram confirmation, is checked in the write transaction.
func (h *HTTP) applyAccountAction(action, id string, in ClientInput, revision *uint64) (c Client, err error) {
	switch action {
	case "create":
		c, err = h.store.Create(in)
	case "edit":
		c, err = h.store.Edit(id, in)
	case "delete":
		err = h.store.deleteRevision(id, revision)
	case "reset":
		err = h.store.resetUsage(id, revision)
		if err == nil {
			h.manager.Disconnect(id)
		}
	case "rotate":
		c, err = h.store.rotate(id, revision)
	case "subscription-rotate":
		_, err = h.store.subscriptionToken(id, true, revision)
	case "disconnect":
		c, err = h.store.Get(id)
		if err == nil && revision != nil && c.Revision != *revision {
			err = ErrConflict
		}
		if err == nil {
			h.manager.Disconnect(id)
			err = h.store.Audit("client.disconnected", id)
		}
	default:
		err = errors.New("unsupported account action")
	}
	if err == nil {
		if e := h.manager.Reconcile(); e != nil {
			err = errAccountRuntimeSync
		}
	}
	return
}

func accountInput(c Client) ClientInput {
	soar, cotten := c.SoarSessionLimit, c.CottenSessionLimit
	return ClientInput{Engines: c.Engines, Name: c.Name, Note: c.Note, Enabled: c.Enabled, QuotaBytes: c.QuotaBytes, ExpiresAt: c.ExpiresAt, RateBytes: c.RateBytes, StreamLimit: c.StreamLimit, SoarSessionLimit: &soar, CottenSessionLimit: &cotten, Revision: c.Revision}
}
