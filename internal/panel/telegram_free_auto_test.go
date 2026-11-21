package panel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTelegramFreeWorkerAutomaticallyApprovesVerifiedReply(t *testing.T) {
	bot, _, _ := newTelegramTest(t)
	bot.now = time.Now
	store := bot.h.store
	mustHandle(t, bot, tgCallback(1, "free:enable"))
	if err := store.changeFreeCatalog(0, func(c *freeCatalog) error {
		c.Plans[0].AutoApprove = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mustHandle(t, bot, freePublic(tgMessage(2, "/free"), 101))
	challenge := bot.freeChallenges[101]
	if challenge.Prompt == 0 {
		t.Fatal("verification prompt was not sent")
	}
	stop := bot.runFreeWorkers(context.Background())
	defer stop()
	reply := freePublic(tgMessage(3, challenge.Code+" I need a connection for this device."), 101)
	bot.opMu.Lock()
	err := bot.handleUpdate(context.Background(), freeReply(reply, challenge.Prompt))
	bot.opMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		req, err := store.freeRequest(101, bot.now())
		if err != nil {
			t.Fatal(err)
		}
		if req.Reviewed && req.AccountID != "" {
			return
		}
	}
	t.Fatal("public worker did not issue the verified request")
}

func TestTelegramFreeAutoApprovalRequiresVerificationAndRespectsCapacity(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	now := time.Now().UTC()
	bot.now = func() time.Time { return now }
	store := bot.h.store
	if err := store.changeFreeCatalog(0, func(c *freeCatalog) error {
		limits := c.limits()
		limits.Issuance = 1
		c.Limits = &limits
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	next := int64(1)
	admin := func(data string) {
		t.Helper()
		mustHandle(t, bot, tgCallback(next, data))
		next++
	}
	public := func(id int64, text string, prompt int64) {
		t.Helper()
		now = now.Add(4 * time.Second)
		u := freePublic(tgMessage(next, text), id)
		if prompt != 0 {
			u = freeReply(u, prompt)
		}
		mustHandle(t, bot, u)
		next++
	}
	request := func(id int64, valid bool) {
		t.Helper()
		public(id, "/free", 0)
		challenge := bot.freeChallenges[id]
		if challenge.Prompt == 0 {
			t.Fatal("verification prompt was not sent")
		}
		code := challenge.Code
		if !valid {
			code += "bad"
		}
		public(id, code+" I need a connection for this device.", challenge.Prompt)
	}
	admin("free:enable")
	request(100, true)
	if req, _ := store.freeRequest(100, now); req.Key == "" || req.Reviewed {
		t.Fatal("a default plan did not stay in manual review")
	}
	admin("free:planedit:master")
	prefix := "free:editor:" + bot.conversation.Key + ":"
	admin(prefix + "approval")
	if c, _ := store.freeCatalog(); c.Plans[0].AutoApprove {
		t.Fatal("editor applied the change before saving")
	}
	admin(prefix + "save")
	request(101, false)
	if req, _ := store.freeRequest(101, now); req.Key != "" {
		t.Fatal("a wrong verification code registered an application")
	}
	request(102, true)
	if req, err := store.freeRequest(102, now); err != nil || !req.Reviewed || req.AccountID == "" ||
		!strings.Contains(fake.texts[len(fake.texts)-1], "approved automatically") {
		t.Fatalf("verified automatic request was not issued: %+v, %v", req, err)
	}
	request(103, true)
	if req, err := store.freeRequest(103, now); err != nil || req.Key == "" || req.Reviewed {
		t.Fatalf("capacity-limited request did not remain pending: %+v, %v", req, err)
	}
	if req, _ := store.freeRequest(100, now); req.Reviewed {
		t.Fatal("enabling automatic approval processed a previous manual request")
	}
}

func TestTelegramFreeAutoApprovalUsesCurrentPolicy(t *testing.T) {
	for _, change := range []string{"pause", "disable", "revision"} {
		t.Run(change, func(t *testing.T) {
			store, now := testStore(t), time.Now()
			if err := store.changeFreeCatalog(0, func(c *freeCatalog) error {
				c.Plans[0].AutoApprove = true
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			catalog, err := store.freeCatalog()
			if err != nil {
				t.Fatal(err)
			}
			nonce := token()[:12]
			if err := store.submitFreePlan(100, nonce, "This is a verified test request.", catalog.Plans[0], now); err != nil {
				t.Fatal(err)
			}
			if err := store.changeFreeCatalog(catalog.Revision, func(c *freeCatalog) error {
				switch change {
				case "pause":
					c.Paused = true
				case "disable":
					c.Plans[0].AutoApprove = false
				case "revision":
					c.Plans[0].Revision++
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.issueFree("100:"+nonce, now, true); !errors.Is(err, errFreePaused) && !errors.Is(err, errFreePlanUnavailable) {
				t.Fatalf("automatic issuance ignored the changed policy: %v", err)
			}
			if req, _ := store.freeRequest(100, now); req.Reviewed || req.AccountID != "" {
				t.Fatal("ineligible request was issued")
			}
		})
	}
}
