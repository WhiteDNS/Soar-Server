package panel

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestTelegramFreeLimitsCanBeChangedByAdministrator(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	store := bot.h.store
	catalog, err := store.freeCatalog()
	if err != nil || catalog.limits().Issuance != 10 {
		t.Fatalf("legacy defaults: %+v, %v", catalog.limits(), err)
	}
	ctx := context.WithValue(context.Background(), telegramActorKey{}, telegramAdministrator{Role: telegramFreeAdmin, ID: 12345})
	if err := bot.freeLimitsMenu(ctx, "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restricted role reached the editor: %v", err)
	}
	mustHandle(t, bot, tgCallback(1, "free:limits:issue"))
	prompt := bot.conversation.Prompt
	mustHandle(t, bot, freeReply(tgMessage(2, "999999"), prompt))
	if c, _ := store.freeCatalog(); c.limits().Issuance != 10 {
		t.Fatal("out-of-range input was saved")
	}
	mustHandle(t, bot, freeReply(tgMessage(3, "۱۰۰"), prompt))
	if c, _ := store.freeCatalog(); c.limits().Issuance != 100 {
		t.Fatal("new issuance limit was not stored")
	}
	mustHandle(t, bot, tgCallback(4, "free:stats"))
	if text := fake.texts[len(fake.texts)-1]; !strings.Contains(text, "Issuance remaining 100/100") {
		t.Fatalf("statistics did not use updated limits: %q", text)
	}
}

func TestTelegramFreeConfiguredLimitsAllow100Issuances(t *testing.T) {
	store := testStore(t)
	now := time.Now().UTC()
	updateLimits := func(change func(*freeLimits)) {
		t.Helper()
		c, err := store.freeCatalog()
		if err != nil {
			t.Fatal(err)
		}
		if err := store.changeFreeCatalog(c.Revision, func(c *freeCatalog) error {
			v := c.limits()
			change(&v)
			c.Limits = &v
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	updateLimits(func(v *freeLimits) {
		v.Issuance, v.Admissions, v.Accounts, v.Streams = 100, 130, 120, 1000
	})
	for i := int64(1); i <= 101; i++ {
		if err := store.submitFree(i, token()[:12], "A request for this test account.", now); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_, req, err := store.freeQueue(now)
		if err != nil || !strings.HasPrefix(req.Key, strconv.FormatInt(i, 10)+":") {
			t.Fatalf("request %d not queued: %v", i, err)
		}
		_, err = store.approveFree(req.Key, now)
		if i == 101 {
			if !errors.Is(err, errFreeDailyLimit) {
				t.Fatalf("expected new daily limit, got %v", err)
			}
		} else if err != nil {
			t.Fatalf("approval %d: %v", i, err)
		}
	}
	if err := store.db.View(inspectFreeRequests); err != nil {
		t.Fatalf("backup validation rejected the updated settings or records: %v", err)
	}
}

func TestTelegramFreeShorterExpiryPreservesRollingAdmissionHistory(t *testing.T) {
	store := testStore(t)
	now := time.Now().UTC()
	if err := store.changeFreeCatalog(0, func(c *freeCatalog) error {
		limits := c.limits()
		limits.Admissions, limits.PendingHours, limits.RetryHours = 2, 1, 1
		c.Limits = &limits
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	submit := func(id int64, at time.Time) error {
		return store.submitFree(id, token()[:12], "A request for this test account.", at)
	}
	if err := submit(100, now); err != nil {
		t.Fatal(err)
	}
	_, req, _ := store.freeQueue(now)
	if err := store.db.Update(func(tx *bolt.Tx) error { return reviewFree(tx, req.Key, "", now, false) }); err != nil {
		t.Fatal(err)
	}
	later := now.Add(2 * time.Hour)
	if err := store.pruneFreeRequests(later); err != nil {
		t.Fatal(err)
	}
	if err := submit(100, later); err != nil {
		t.Fatalf("shorter retry window did not take effect: %v", err)
	}
	later = later.Add(2 * time.Hour)
	if count, _, err := store.freeQueue(later); err != nil || count != 0 {
		t.Fatalf("shorter pending lifetime did not take effect: %d, %v", count, err)
	}
	if err := submit(101, later); !errors.Is(err, errFreeAdmissionLimit) {
		t.Fatalf("a short retry or expiry bypassed admission history: %v", err)
	}
}
