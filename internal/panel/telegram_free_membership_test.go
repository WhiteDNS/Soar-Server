package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTelegramRequiredMembershipAndApplicantUsername(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	now := time.Now().UTC()
	bot.now = func() time.Time { return now }
	var mu sync.Mutex
	joined, botAdmin, memberAPIUnavailable, calls := false, true, false, 0
	setJoined := func(v bool) { mu.Lock(); joined = v; mu.Unlock() }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getChat"):
			var body struct {
				Chat any `json:"chat_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Chat == "@required_channel" {
				_, _ = fmt.Fprint(w, `{"ok":true,"result":{"id":-1001,"type":"channel","title":"Test channel","username":"required_channel"}}`)
			} else {
				_, _ = fmt.Fprint(w, `{"ok":true,"result":{"id":-1002,"type":"supergroup","title":"Private group"}}`)
			}
		case strings.HasSuffix(r.URL.Path, "/getChatMember"):
			var body struct {
				Chat int64 `json:"chat_id"`
				User int64 `json:"user_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			defer mu.Unlock()
			calls++
			if memberAPIUnavailable {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = fmt.Fprint(w, `{"ok":false,"error_code":503}`)
				return
			}
			status, isMember := "member", true
			if body.User == 9988 {
				status = "administrator"
				if !botAdmin {
					status = "member"
				}
			} else if body.Chat == -1002 {
				status, isMember = "restricted", joined
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{
				"status": status, "is_member": isMember, "user": map[string]int64{"id": body.User},
			}})
		default:
			fake.serve(w, r)
		}
	}))
	defer server.Close()
	bot.baseURL, bot.client = server.URL, server.Client()

	next := int64(1)
	handle := func(u telegramUpdate) { t.Helper(); mustHandle(t, bot, u); next++; now = now.Add(4 * time.Second) }
	admin := func(data string) { handle(tgCallback(next, data)) }
	public := func(text string, prompt int64) {
		u := freePublic(tgMessage(next, text), 888)
		u.Message.From.Username = "test_member"
		if prompt != 0 {
			u = freeReply(u, prompt)
		}
		handle(u)
	}
	admin("free:enable")
	admin("free:membership:add")
	handle(freeReply(tgMessage(next, "@required_channel"), bot.conversation.Prompt))
	admin("free:membership:add")
	handle(freeReply(tgMessage(next, "-1002 https://t.me/+Invite_example"), bot.conversation.Prompt))
	admin("free:membership:on")
	catalog, err := bot.h.store.freeCatalog()
	if err != nil || !catalog.JoinRequired || len(catalog.JoinChats) != 2 {
		t.Fatalf("required chats not saved: %+v, %v", catalog.JoinChats, err)
	}
	restricted := context.WithValue(context.Background(), telegramActorKey{}, telegramAdministrator{ID: 111, Role: telegramFullAdmin})
	if err := bot.freeMembershipMenu(restricted, "off", ""); !errors.Is(err, errTelegramAdminPermission) {
		t.Fatalf("delegated admin changed membership: %v", err)
	}
	mu.Lock()
	before := calls
	mu.Unlock()
	public("/start", 0)
	mu.Lock()
	after := calls
	mu.Unlock()
	if before != after {
		t.Fatal("start unnecessarily verified membership")
	}
	public("/free", 0)
	k := fake.keyboards[len(fake.keyboards)-1]
	if k.Rows[0][0].URL != "https://t.me/+Invite_example" || !strings.Contains(fake.texts[len(fake.texts)-1], "Join every listed chat") {
		t.Fatal("missing group was not shown")
	}
	setJoined(true)
	handle(freePublic(tgCallback(next, "free:joincheck"), 888))
	plan := fake.keyboards[len(fake.keyboards)-1].Rows[0][0].Data
	if !strings.HasPrefix(plan, "free:plan:") {
		t.Fatalf("recheck did not open plans: %s", plan)
	}
	handle(freePublic(tgCallback(next, plan), 888))
	challenge := bot.freeChallenges[888]
	if challenge.Prompt == 0 {
		t.Fatal("member could not get a code")
	}
	setJoined(false)
	response := challenge.Code + " I need the service for this test."
	public(response, challenge.Prompt)
	if count, _, _ := bot.h.store.freeQueue(now); count != 0 {
		t.Fatal("leaving after seeing plans bypassed submission check")
	}
	setJoined(true)
	public(response, challenge.Prompt)
	_, req, err := bot.h.store.freeQueue(now)
	if err != nil || req.Username != "test_member" {
		t.Fatalf("request or username missing: %v", err)
	}
	admin("free:queue")
	if label := fake.keyboards[len(fake.keyboards)-1].Rows[0][0].Text; !strings.Contains(label, "888 · @test_member") {
		t.Fatalf("queue applicant label %q", label)
	}
	admin("free:requestdetail:" + req.Key)
	if !strings.Contains(fake.texts[len(fake.texts)-1], "888 · @test_member") {
		t.Fatal("request details omitted username")
	}
	if err := bot.sendFreeRequestDigest(context.Background(), bot.config(), now); err != nil || !strings.Contains(fake.texts[len(fake.texts)-1], "888 · @test_member") {
		t.Fatalf("digest omitted username: %v", err)
	}
	setJoined(false)
	admin("free:approve:" + req.Key + "@" + bot.freeEpoch)
	if !strings.Contains(fake.texts[len(fake.texts)-1], "must join") {
		t.Fatal("approval did not explain missing membership")
	}
	if count, _, _ := bot.h.store.freeQueue(now); count != 1 {
		t.Fatal("former member's request was approved")
	}
	setJoined(true)
	mu.Lock()
	memberAPIUnavailable = true
	mu.Unlock()
	admin("free:approve:" + req.Key + "@" + bot.freeEpoch)
	if !strings.Contains(fake.texts[len(fake.texts)-1], "could not verify membership") {
		t.Fatal("Telegram outage was not distinguished from nonmembership")
	}
	if count, _, _ := bot.h.store.freeQueue(now); count != 1 {
		t.Fatal("Telegram outage bypassed the approval check")
	}
	mu.Lock()
	memberAPIUnavailable = false
	mu.Unlock()
	admin("free:approve:" + req.Key + "@" + bot.freeEpoch)
	if account, err := bot.h.store.freeRequest(888, now); err != nil || account.AccountID == "" {
		t.Fatalf("approval failed after rejoining: %v", err)
	}
	mu.Lock()
	botAdmin = false
	mu.Unlock()
	var demotion telegramUpdate
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"update_id":%d,"my_chat_member":{"chat":{"id":-1002},"new_chat_member":{"status":"member","user":{"id":9988,"is_bot":true}}}}`, next)), &demotion); err != nil {
		t.Fatal(err)
	}
	handle(demotion)
	admin("free:membership:check")
	public("/free", 0)
	// The existing account remains available. A new applicant needs a working membership verifier.
	handle(freePublic(tgMessage(next, "/free"), 999))
	if !strings.Contains(fake.texts[len(fake.texts)-1], "could not verify membership") {
		t.Fatal("lost bot access was not distinguished from nonmembership")
	}
	admin("free:membership:remove:-1002")
	admin("free:membership:remove:-1001")
	if c, _ := bot.h.store.freeCatalog(); c.JoinRequired || len(c.JoinChats) != 0 {
		t.Fatal("removing the last chat did not turn the gate off")
	}
}

func TestTelegramRequiredMembershipProofIsBoundToPolicyAndUser(t *testing.T) {
	store, now := testStore(t), time.Now()
	item := freeRequiredChat{ID: -1001, Title: "Channel", URL: "https://t.me/required_channel"}
	if err := store.changeFreeCatalog(0, func(c *freeCatalog) error {
		c.JoinRequired, c.JoinRevision, c.JoinChats = true, 1, []freeRequiredChat{item}
		c.Plans[0].AutoApprove = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	c, _ := store.freeCatalog()
	nonce := token()[:12]
	reason := "A valid reason for this service."
	proof := freeMembershipProof{policy: c.joinPolicy(), user: 101, expires: now.Add(time.Minute)}
	if err := store.submitFreePlan(101, nonce, reason, c.Plans[0], now); !errors.Is(err, errFreeJoinMissing) {
		t.Fatalf("submission without proof: %v", err)
	}
	if err := store.submitFreeApplication(102, nonce, reason, "", c.Plans[0], now, proof); !errors.Is(err, errFreeJoinMissing) {
		t.Fatalf("submission used another account's proof: %v", err)
	}
	if err := store.submitFreeApplication(101, nonce, reason, "", c.Plans[0], now, proof); err != nil {
		t.Fatal(err)
	}
	if err := store.changeFreeCatalog(c.Revision, func(c *freeCatalog) error { c.JoinRevision++; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := store.issueFree("101:"+nonce, now, true, proof); !errors.Is(err, errFreeJoinMissing) {
		t.Fatalf("changed policy accepted stale proof for automatic approval: %v", err)
	}
	c, _ = store.freeCatalog()
	proof.policy = c.joinPolicy()
	if _, err := store.issueFree("101:"+nonce, now, true, proof); err != nil {
		t.Fatal(err)
	}
}

func TestTelegramRequiredMembershipSlowLookupDoesNotBlockStart(t *testing.T) {
	bot, fake, _ := newTelegramTest(t)
	mustHandle(t, bot, tgCallback(1, "free:enable"))
	if err := bot.h.store.changeFreeCatalog(0, func(c *freeCatalog) error {
		c.JoinRequired, c.JoinRevision = true, 1
		c.JoinChats = []freeRequiredChat{{ID: -1001, Title: "Channel", URL: "https://t.me/required_channel"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getChatMember") {
			entered <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		fake.serve(w, r)
	}))
	defer server.Close()
	defer close(release)
	bot.baseURL, bot.client = server.URL, server.Client()
	stop := bot.runFreeWorkers(context.Background())
	defer stop()
	mustHandle(t, bot, freePublic(tgMessage(2, "/free"), 888))
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("verification worker did not start")
	}
	mustHandle(t, bot, freePublic(tgMessage(3, "/start"), 999))
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		fake.mu.Lock()
		menu := false
		for _, id := range fake.chatIDs {
			menu = menu || id == 999
		}
		fake.mu.Unlock()
		if menu {
			return
		}
	}
	t.Fatal("membership lookup blocked a different user's /start")
}
