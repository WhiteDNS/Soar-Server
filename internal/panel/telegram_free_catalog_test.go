package panel

import (
	"errors"
	"testing"
	"time"
)

func TestTelegramFreePlanSnapshotAndWithdrawal(t *testing.T) {
	bot, _, _ := newTelegramTest(t)
	s, now := bot.h.store, bot.now()
	p := defaultFreePlan()
	p.Input.Engines, p.Input.QuotaBytes, p.Days = "all", 3<<30, 11
	if err := s.changeFreeCatalog(0, func(c *freeCatalog) error {
		c.Plans[0] = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.submitFreePlan(1, token()[:12], "A real test application", p, now); err != nil {
		t.Fatal(err)
	}
	_, req, err := s.freeQueue(now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.changeFreeCatalog(1, func(c *freeCatalog) error {
		c.Allowed = "masterdns"
		c.Plans[0].Input.QuotaBytes = 9 << 30
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.approveFree(req.Key, now); !errors.Is(err, errFreePlanUnavailable) {
		t.Fatalf("withdrawn engine was approved: %v", err)
	}
	if err = s.changeFreeCatalog(2, func(c *freeCatalog) error {
		c.Allowed = "all"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c, err := s.approveFree(req.Key, now)
	if err != nil || c.Engines != "all" || c.QuotaBytes != 3<<30 || c.ExpiresAt != now.Add(11*24*time.Hour).UnixMilli() {
		t.Fatalf("issued terms did not match the request: %+v, %v", c, err)
	}
}

func TestTelegramFreePlansSupportEachEngineCombination(t *testing.T) {
	bot, _, _ := newTelegramTest(t)
	s, now := bot.h.store, bot.now()
	var plans []freePlan
	for _, engine := range []string{"soar", "cotten", "masterdns", "both", "soar_masterdns", "cotten_masterdns", "all"} {
		p := defaultFreePlan()
		p.ID, p.Input.Engines, p.Input.Name = token()[:8], engine, engine
		choice, err := encodeFreeEngines(engineSelections[engine])
		if err != nil || choice != engine {
			t.Fatalf("engine combination %s not editable: %v", engine, err)
		}
		plans = append(plans, p)
	}
	if err := s.changeFreeCatalog(0, func(c *freeCatalog) error { c.Plans = plans; return nil }); err != nil {
		t.Fatal(err)
	}
	for i, p := range plans {
		id, key := int64(i+1), token()[:12]
		if err := s.submitFreePlan(id, key, "Testing this engine combination", p, now); err != nil {
			t.Fatal(err)
		}
		_, r, err := s.freeQueue(now)
		if err != nil || r.Key == "" {
			t.Fatalf("no pending plan: %v", err)
		}
		a, err := s.approveFree(r.Key, now)
		if err != nil || a.Engines != p.Input.Engines {
			t.Fatalf("wrong engines issued: %s, %v", a.Engines, err)
		}
		links, err := s.Profiles(a.ID)
		if err != nil || len(links) != len(engineSelections[a.Engines]) {
			t.Fatalf("wrong profile count for %s: %d, %v", a.Engines, len(links), err)
		}
	}
}

func TestTelegramFreeStreamBudgetCountsActiveAccounts(t *testing.T) {
	bot, _, _ := newTelegramTest(t)
	s, now := bot.h.store, bot.now()
	p := defaultFreePlan()
	p.Input.StreamLimit = 128
	if e := s.changeFreeCatalog(0, func(c *freeCatalog) error { c.Plans[0] = p; return nil }); e != nil {
		t.Fatal(e)
	}
	var first Client
	for id := int64(1); id <= 5; id++ {
		if e := s.submitFreePlan(id, token()[:12], "Testing free stream capacity", p, now); e != nil {
			t.Fatal(e)
		}
		_, r, _ := s.freeQueue(now)
		a, e := s.approveFree(r.Key, now)
		if id == 5 {
			if !errors.Is(e, errFreeCapacity) {
				t.Fatalf("stream budget bypassed: %v", e)
			}
			in := accountInput(first)
			in.Enabled = false
			if _, e := s.Edit(first.ID, in); e != nil {
				t.Fatal(e)
			}
			if _, e := s.approveFree(r.Key, now); e != nil {
				t.Fatalf("suspended account still used stream capacity: %v", e)
			}
		} else if e != nil {
			t.Fatal(e)
		}
		if id == 1 {
			first = a
		}
	}
}
