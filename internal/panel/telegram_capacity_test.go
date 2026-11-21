package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTelegramPublicBurst500(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	bot.now = time.Now
	mustHandle(t, bot, tgCallback(1, "free:enable"))
	mustHandle(t, bot, freePublic(tgMessage(2, "/free"), 888))
	challenge := bot.freeChallenges[888]
	if challenge.Prompt == 0 {
		t.Fatal("verification was not prepared")
	}
	if err := bot.h.store.submitFree(999, token()[:12], "Need access for my phone", bot.now()); err != nil {
		t.Fatal(err)
	}
	_, request, _ := bot.h.store.freeQueue(bot.now())
	mustHandle(t, bot, tgCallback(3, "free:approve:"+request.Key+"@"+bot.freeEpoch))

	fake.mu.Lock()
	fake.texts, fake.chatIDs = nil, nil
	for i := range int64(500) {
		fake.updates = append(fake.updates, freePublic(tgMessage(10+i, "/start"), 10000+i))
	}
	for i := range int64(100) {
		fake.updates = append(fake.updates, freePublic(tgMessage(510+i, "/start"), 10499))
	}
	reply := freeReply(freePublic(tgMessage(611, challenge.Code+" I need this for my connection."), 888), challenge.Prompt)
	fake.updates = append(fake.updates, tgMessage(610, "/menu"), reply, freePublic(tgMessage(612, "/free_config"), 999))
	fake.mu.Unlock()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			select {
			case <-time.After(15 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		fake.serve(w, r)
	}))
	defer server.Close()
	bot.baseURL, bot.client, bot.sendInterval = server.URL, server.Client(), 1100*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 36*time.Second)
	done := make(chan struct{})
	start := time.Now()
	go func() { defer close(done); bot.Run(ctx) }()
	defer func() { cancel(); <-done }()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	var admin, verification, existing, first, threeHundred time.Duration
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("burst did not finish: sent=%d, rejected=%d, failed=%d, admin=%s, verification=%s, existing=%s",
				bot.freeTraffic.sent.Load(), bot.freeTraffic.rejected.Load(), bot.freeTraffic.failed.Load(), admin, verification, existing)
		case <-tick.C:
		}
		fake.mu.Lock()
		seen := make(map[int64]bool, 500)
		duplicate := false
		for i, id := range fake.chatIDs {
			switch {
			case id == 12345 && strings.Contains(fake.texts[i], "server management"):
				if admin == 0 {
					admin = time.Since(start)
				}
			case id == 888 && strings.Contains(fake.texts[i], "request was registered"):
				if verification == 0 {
					verification = time.Since(start)
				}
			case id == 999 && strings.Contains(fake.texts[i], "masterdns://"):
				if existing == 0 {
					existing = time.Since(start)
				}
			case id >= 10000 && id < 10500:
				duplicate = duplicate || seen[id]
				seen[id] = true
			}
		}
		fake.mu.Unlock()
		if duplicate {
			t.Fatal("duplicate menu for a repeated /start")
		}
		if len(seen) > 0 && first == 0 {
			first = time.Since(start)
		}
		if len(seen) >= 300 && threeHundred == 0 {
			threeHundred = time.Since(start)
		}
		if len(seen) == 500 && admin > 0 && verification > 0 && existing > 0 {
			t.Logf("first=%s, 300th=%s, all 500=%s, admin=%s, verification=%s, existing config=%s",
				first, threeHundred, time.Since(start), admin, verification, existing)
			if time.Since(start) > 35*time.Second || admin > 3*time.Second || verification > 3*time.Second || existing > 3*time.Second {
				t.Fatal("burst or priority lane missed its latency target")
			}
			if bot.freeTraffic.rejected.Load() != 0 || bot.freeTraffic.merged.Load() != 100 {
				t.Fatal("burst rejected or failed to deduplicate updates")
			}
			if pending, _, _ := bot.h.store.freeQueue(bot.now()); pending != 1 {
				t.Fatalf("want one verified request, got %d", pending)
			}
			return
		}
	}
}

func TestTelegramPublicMenuRetriesRateLimit(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	mustHandle(t, bot, tgCallback(1, "free:enable"))
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") && attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": false, "error_code": 429, "parameters": map[string]int{"retry_after": 1},
			})
			return
		}
		fake.serve(w, r)
	}))
	defer server.Close()
	bot.baseURL, bot.client = server.URL, server.Client()
	stop := bot.runFreeWorkers(context.Background())
	defer stop()
	start := time.Now()
	mustHandle(t, bot, freePublic(tgMessage(2, "/start"), 888))
	for elapsed := time.Since(start); elapsed < 3*time.Second; elapsed = time.Since(start) {
		if bot.freeTraffic.sent.Load() == 1 {
			if elapsed < time.Second || attempts.Load() != 2 || bot.freeTraffic.limited.Load() != 1 {
				t.Fatalf("incorrect retry: elapsed=%s attempts=%d", elapsed, attempts.Load())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("menu was lost after 429")
}

func TestTelegramPublicMenuOverflowAndEmergencyStop(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	mustHandle(t, bot, tgCallback(1, "free:enable"))
	bot.deferAPI(withFreeLane(context.Background(), freeMenuLane), 2)
	stop := bot.runFreeWorkers(context.Background())
	defer stop()
	for i := range int64(freeMenuCapacity + freeMenuWorkers + 20) {
		mustHandle(t, bot, freePublic(tgMessage(2+i, "/start"), 10000+i))
	}
	if bot.freeTraffic.admitted.Load() > freeMenuCapacity+freeMenuWorkers || bot.freeTraffic.rejected.Load() < 20 {
		t.Fatal("public queue exceeded its capacity")
	}
	mustHandle(t, bot, tgCallback(1000, "free:disable"))
	mustHandle(t, bot, freePublic(tgMessage(1001, "/start"), 20000))
	mustHandle(t, bot, tgCallback(1002, "free:enable"))
	deadline := time.Now().Add(4 * time.Second)
	for len(bot.freeQueues[2]) != 0 && time.Now().Before(deadline) {
		time.Sleep(15 * time.Millisecond)
	}
	mustHandle(t, bot, freePublic(tgMessage(1003, "/start"), 20001))

	for time.Now().Before(deadline) {
		fake.mu.Lock()
		fresh := false
		for i, id := range fake.chatIDs {
			if (id >= 10000 && id < 11000 || id == 20000) && strings.Contains(fake.texts[i], "choose a plan") {
				fake.mu.Unlock()
				t.Fatal("stale menu was sent after the emergency stop")
			}
			fresh = fresh || id == 20001
		}
		fake.mu.Unlock()
		if fresh {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("new updates did not resume after public access was re-enabled")
}
