package panel

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	bolt "go.etcd.io/bbolt"
)

const freePlanLimit = 12

var (
	freeCatalogKey         = []byte("telegram_free_catalog")
	errFreePlanUnavailable = errors.New("the free plan or an engine is unavailable; review the current plans")
	errFreePaused          = errors.New("free registration is paused")
	errFreeCapacity        = errors.New("reserved panel capacity reached for free accounts")
)

type freePlan struct {
	ID          string
	Revision    uint64
	Published   bool
	AutoApprove bool `json:",omitempty"`
	Days        int
	Input       ClientInput
}

type freeCatalog struct {
	Revision    uint64
	Allowed     string
	Paused      bool
	RestoreHold bool
	Plans       []freePlan
	Text        map[string]string
	Limits      *freeLimits `json:",omitempty"`
}

func defaultFreePlan() freePlan {
	soar, cotten, master := 2, 2, 2
	return freePlan{ID: "master", Revision: 1, Published: true, Days: 7, Input: ClientInput{
		Name: "MasterDNS", Engines: "masterdns", Enabled: true, QuotaBytes: freeQuotaBytes,
		RateBytes: 512 << 10, StreamLimit: 8, SoarSessionLimit: &soar,
		CottenSessionLimit: &cotten, MasterDNSSessionLimit: &master,
	}}
}

func (p freePlan) validate() error {
	invalidID := strings.ContainsFunc(p.ID, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	})
	if p.ID == "" || len(p.ID) > 12 || invalidID || p.Revision == 0 ||
		p.Days < 1 || p.Days > 365 || p.Input.Engines == "" || p.Input.QuotaBytes < 1 ||
		p.Input.QuotaBytes > 1<<40 || p.Input.RateBytes < 1 || p.Input.StreamLimit < 1 || p.Input.StreamLimit > 128 ||
		p.Input.SoarSessionLimit == nil || p.Input.CottenSessionLimit == nil || p.Input.MasterDNSSessionLimit == nil ||
		utf8.RuneCountInString(p.Input.Name) > 48 || len(p.Input.Name) > 140 || len(p.Input.Note) > 1000 ||
		strings.ContainsAny(p.Input.Name+p.Input.Note, "\r\n") {
		return errors.New("invalid plan: use a name, a finite quota, 1–365 days, a speed limit and 1–128 streams")
	}
	return p.Input.validate()
}

func (p freePlan) account(chat string, now time.Time) ClientInput {
	in := p.Input
	in.Name = "Telegram · " + p.Input.Name
	in.Note = "Telegram ID " + chat
	in.Enabled = true
	in.ExpiresAt = now.Add(time.Duration(p.Days) * 24 * time.Hour).UnixMilli()
	return in
}

func (c freeCatalog) plan(id string) (freePlan, bool) {
	for _, p := range c.Plans {
		if p.ID == id {
			return p, true
		}
	}
	return freePlan{}, false
}

func (c freeCatalog) offers(p freePlan) bool {
	if !p.Published || c.Allowed == "" {
		return false
	}
	engines := engineSelections[p.Input.Engines]
	for _, engine := range engines {
		if !slices.Contains(engineSelections[c.Allowed], engine) {
			return false
		}
	}
	return len(engines) > 0
}

func (c freeCatalog) validate() error {
	if !validEngines(c.Allowed) || len(c.Plans) > freePlanLimit || len(c.Text) > 6 {
		return errors.New("invalid free configuration catalogue")
	}
	if c.Limits != nil {
		for _, field := range freeLimitFields {
			if n := *field.value(c.Limits); n < field.min || n > field.max {
				return errors.New("invalid free configuration limits")
			}
		}
	}
	ids := make(map[string]bool)
	for _, p := range c.Plans {
		if e := p.validate(); e != nil {
			return e
		}
		if ids[p.ID] {
			return errors.New("duplicate free plan")
		}
		ids[p.ID] = true
	}
	for k, v := range c.Text {
		if !validFreeTextField(k) || len(v) > 2400 {
			return errors.New("invalid free configuration text")
		}
	}
	return nil
}

func validFreeTextField(k string) bool {
	return slices.Contains([]string{"welcome:fa", "welcome:en", "terms:fa", "terms:en", "guide:fa", "guide:en"}, k)
}

func readFreeCatalog(tx *bolt.Tx) (freeCatalog, error) {
	raw := tx.Bucket(meta).Get(freeCatalogKey)
	if raw == nil {
		return freeCatalog{Allowed: "all", Plans: []freePlan{defaultFreePlan()}}, nil
	}
	var c freeCatalog
	e := json.Unmarshal(raw, &c)
	return c, e
}

func (s *Store) freeCatalog() (c freeCatalog, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		c, err = readFreeCatalog(tx)
		return err
	})
	return
}

func (s *Store) changeFreeCatalog(revision uint64, change func(*freeCatalog) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		c, e := readFreeCatalog(tx)
		if e != nil {
			return e
		}
		if c.Revision != revision {
			return ErrConflict
		}
		if e = change(&c); e != nil {
			return e
		}
		return saveFreeCatalog(tx, c)
	})
}

func saveFreeCatalog(tx *bolt.Tx, c freeCatalog) error {
	if e := c.validate(); e != nil {
		return e
	}
	c.Revision++
	return putJSON(tx.Bucket(meta), freeCatalogKey, c)
}
