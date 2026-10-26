package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func configureTestAssistant(t *testing.T, b *browser, base, protocol string) map[string]any {
	t.Helper()
	var view map[string]any
	if err := json.Unmarshal(b.request("/api/assistant/config", "GET", nil, 200), &view); err != nil {
		t.Fatal(err)
	}
	raw := b.request("/api/assistant/config", "PUT", map[string]any{"enabled": true, "provider": "custom", "protocol": protocol, "base_url": base, "model": "test-model", "api_key": "test-private-ai-key", "max_tokens": 4096, "json_mode": true, "revision": view["revision"]}, 200)
	if bytes.Contains(raw, []byte("test-private-ai-key")) {
		t.Fatal("AI key returned to browser")
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func testChatInput(view map[string]any) map[string]any {
	return map[string]any{"message": "Investigate connection failures", "include_context": true, "consent": true, "revision": view["revision"], "destination": view["base_url"]}
}

func TestAssistantProviderProtocols(t *testing.T) {
	for _, protocol := range []string{"openai", "gemini", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				raw, _ := io.ReadAll(r.Body)
				if !bytes.Contains(raw, []byte("system instruction")) || !bytes.Contains(raw, []byte("question")) {
					t.Error("missing prompt")
				}
				if bytes.Contains(raw, []byte("private-api-key")) {
					t.Error("key entered model prompt")
				}
				switch protocol {
				case "gemini":
					if r.URL.Path != "/models/test-model:generateContent" || r.Header.Get("x-goog-api-key") != "private-api-key" {
						t.Error("Gemini wire protocol")
					}
					jsonReply(w, 200, map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]string{"text": "answer"}}}}}})
				case "anthropic":
					if r.URL.Path != "/messages" || r.Header.Get("x-api-key") != "private-api-key" || r.Header.Get("anthropic-version") == "" {
						t.Error("Anthropic wire protocol")
					}
					jsonReply(w, 200, map[string]any{"stop_reason": "end_turn", "content": []any{map[string]string{"type": "text", "text": "answer"}}})
				default:
					if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer private-api-key" {
						t.Error("compatible wire protocol")
					}
					jsonReply(w, 200, map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": "answer"}}}})
				}
			}))
			defer server.Close()
			cfg := AssistantConfig{Provider: "custom", Protocol: protocol, BaseURL: server.URL, Model: "test-model", APIKey: "private-api-key", JSONMode: true, MaxTokens: 4096}
			if err := cfg.validate(); err != nil {
				t.Fatal(err)
			}
			if _, err := callAssistant(context.Background(), newAssistantClient(), cfg, "https://another.example", "system instruction", nil); err == nil {
				t.Fatal("unconfirmed endpoint accepted")
			}
			if calls.Load() != 0 {
				t.Fatal("unconfirmed network request")
			}
			answer, err := callAssistant(context.Background(), newAssistantClient(), cfg, server.URL, "system instruction", []assistantMessage{{Role: "user", Content: "question"}})
			if err != nil || answer != "answer" {
				t.Fatal(answer, err)
			}
		})
	}
}

func TestAssistantAnalysisConsentAndApprovedSettings(t *testing.T) {
	b, s, h := testBrowser(t)
	b.request("/api/assistant/config", "GET", nil, 401)
	b.request("/api/logs", "GET", nil, 401)
	b.login()
	account := account(t, s, 0)
	h.manager.logEvent("error", "cotten", account.ID, "connection_failed", context.DeadlineExceeded, time.Second)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		for _, secret := range []string{account.ID, account.Name, account.Zone, account.PublicKey, "test-private-ai-key", "sk-shouldneverleave123"} {
			if bytes.Contains(body, []byte(secret)) {
				t.Errorf("prompt leaked %q", secret)
			}
		}
		if !bytes.Contains(body, []byte("connection_failed")) || !bytes.Contains(body, []byte("account-")) {
			t.Error("diagnostic context missing")
		}
		content := `{"answer":"Dial timeouts are visible; client measurements are still needed.","reason":"Test a smaller inflight window.","changes":{"soarSettings.maxInflight":64}}`
		jsonReply(w, 200, map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content}}}})
	}))
	defer provider.Close()
	view := configureTestAssistant(t, b, provider.URL, "openai")
	request := testChatInput(view)
	request["message"] = "Investigate timeout api_key=sk-shouldneverleave123"
	request["consent"] = false
	b.request("/api/assistant/chat", "POST", request, 400)
	if calls.Load() != 0 {
		t.Fatal("sent without consent")
	}
	request["consent"] = true
	request["destination"] = "https://another.example"
	b.request("/api/assistant/chat", "POST", request, 409)
	request["destination"] = provider.URL
	before, _ := s.Settings()
	var response struct {
		Proposal *assistantProposal `json:"proposal"`
		Answer   string             `json:"answer"`
	}
	if err := json.Unmarshal(b.request("/api/assistant/chat", "POST", request, 200), &response); err != nil || response.Proposal == nil {
		t.Fatal("missing proposal", err)
	}
	after, _ := s.Settings()
	if !reflectSettings(before, after) {
		t.Fatal("chat changed settings")
	}
	id := response.Proposal.ID
	b.request("/api/assistant/apply", "POST", map[string]any{"id": id, "confirm": false}, 400)
	// A second authenticated browser cannot approve this browser's proposal.
	jar, _ := cookiejar.New(nil)
	jarBrowser := &browser{t: t, url: b.url, client: &http.Client{Jar: jar}}
	jarBrowser.login()
	jarBrowser.request("/api/assistant/apply", "POST", map[string]any{"id": id, "confirm": true}, 409)
	var applied struct {
		Undo    *assistantProposal `json:"undo"`
		Runtime bool               `json:"runtime_synchronized"`
	}
	if err := json.Unmarshal(b.request("/api/assistant/apply", "POST", map[string]any{"id": id, "confirm": true}, 200), &applied); err != nil || applied.Undo == nil || !applied.Runtime {
		t.Fatal("apply", err)
	}
	after, _ = s.Settings()
	if after.profileSettings().MaxInflight != 64 {
		t.Fatal("not applied")
	}
	b.request("/api/assistant/apply", "POST", map[string]any{"id": id, "confirm": true}, 409)
	b.request("/api/assistant/undo", "POST", map[string]any{"id": applied.Undo.ID, "confirm": true}, 200)
	after, _ = s.Settings()
	if !reflectSettings(before, after) {
		t.Fatal("undo changed unrelated settings")
	}
	// Machine-local provider credentials never enter the normal settings backup.
	var backup bytes.Buffer
	if err := s.Backup(&backup); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(backup.Bytes(), []byte("test-private-ai-key")) {
		t.Fatal("backup contains AI credential")
	}
}

func reflectSettings(a, b Settings) bool {
	x, _ := json.Marshal(normalizedAssistantSettings(a))
	y, _ := json.Marshal(normalizedAssistantSettings(b))
	return bytes.Equal(x, y)
}

func TestAssistantProposalValidationAndAtomicConflict(t *testing.T) {
	s := testStore(t)
	cfg, _ := s.Settings()
	for _, raw := range []string{`{"base_zone":"another.example"}`, `{"soarSettings.maxInflight":0}`, `{"soarSettings.transport":"bad"}`, `{"resolvers":["127.0.0.1"]}`, `{"soarSettings.maxInflight":null}`, `{"server_stream_limit":1.5}`, `{"soarSettings.maxAnswer":"512"}`, `{"antiflood":{"mode":"off"}}`} {
		var patch map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &patch)
		if _, err := buildAssistantProposal(cfg, patch, ""); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	var patch map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"country":"GLOBAL","soarSettings.maxInflight":64}`), &patch)
	proposal, err := buildAssistantProposal(cfg, patch, "")
	if err != nil || proposal.After.Country != "" {
		t.Fatal("global proposal", err)
	}
	changed := cfg
	changed.ServerStreamLimit = 12
	if err = s.SaveSettings(changed); err != nil {
		t.Fatal(err)
	}
	if err = s.saveAssistantSettings(cfg, proposal.After, "assistant.settings_applied"); !errors.Is(err, ErrConflict) {
		t.Fatal("did not detect concurrent settings save", err)
	}
	got, _ := s.Settings()
	if got.ServerStreamLimit != 12 || got.profileSettings().MaxInflight != cfg.profileSettings().MaxInflight {
		t.Fatal("conflict overwrote settings")
	}
}

func TestDiagnosticLogsCaptureConnectionAndPersist(t *testing.T) {
	b, s, h := testBrowser(t)
	b.login()
	c := account(t, s, 0)
	m := h.manager
	if err := m.Reconcile(); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	rt := m.accounts[c.ID]
	m.mu.RUnlock()
	m.dialOverride = func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("198.51.100.77"), Port: 443}, Err: syscall.ECONNREFUSED}
	}
	if _, err := m.dialForEngine(rt, context.Background(), "tcp", "198.51.100.77:443", upstreamMasterDNS); err == nil {
		t.Fatal("expected failed dial")
	}
	raw := b.request("/api/logs?source=masterdns&level=error", "GET", nil, 200)
	if !bytes.Contains(raw, []byte("connection_refused")) || !bytes.Contains(raw, []byte(c.ID)) {
		t.Fatal("missing contextual failure", string(raw))
	}
	for _, secret := range []string{"198.51.100.77", c.Name, c.Zone, c.PublicKey} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("log leaked %q", secret)
		}
	}
	b.request("/api/logs?limit=2001", "GET", nil, 400)
	b.request("/api/logs?since=invalid", "GET", nil, 400)
	m.logs.flush()
	var restored diagnosticLogs
	restored.open(diagnosticLogPath(s))
	entries, _, _ := restored.snapshot(logQuery{Source: "masterdns", Level: "error"})
	if len(entries) != 1 || entries[0].Reason != "connection_refused" {
		t.Fatal("logs not persisted")
	}
	if info, err := os.Stat(diagnosticLogPath(s)); err != nil || info.Size() == 0 {
		t.Fatal("no log file", err)
	}
}

func TestDiagnosticLogBurstBoundsAndFiltering(t *testing.T) {
	var logs diagnosticLogs
	for range 1000 {
		logs.add(DiagnosticLog{Level: "warn", Source: "soar", Code: "dns_queue_full", AccountID: "one"})
	}
	entries, retained, coalesced := logs.snapshot(logQuery{})
	if retained != 1 || coalesced != 999 || entries[0].Count != 1000 {
		t.Fatal("burst was not coalesced")
	}
	for i := range 2100 {
		logs.add(DiagnosticLog{Level: "error", Source: "cotten", Code: fmt.Sprintf("event_%d", i)})
	}
	entries, retained, _ = logs.snapshot(logQuery{Source: "cotten", Search: "event_2099"})
	if retained != diagnosticLogCapacity || len(entries) != 1 {
		t.Fatal("retention or filtering broken")
	}
}

func TestAssistantConfigValidationAndRedaction(t *testing.T) {
	for _, base := range []string{"http://public.example/v1", "https://169.254.169.254/v1", "https://10.0.0.1/v1", "https://user:pass@example.org/v1", "https://example.org/v1?key=secret", "https://example.org/../admin"} {
		c := AssistantConfig{Provider: "custom", Protocol: "openai", BaseURL: base, Model: "test", MaxTokens: 4096}
		if c.validate() == nil {
			t.Fatalf("accepted %s", base)
		}
	}
	text := assistantRedact("password=hello sk-1234567890xyz https://example.org/sub/key "+`{"private_key":"private-test-value","api_key":"api-test-value"}`, AssistantConfig{})
	for _, secret := range []string{"hello", "sk-1234567890xyz", "/sub/key", "private-test-value", "api-test-value"} {
		if strings.Contains(text, secret) {
			t.Fatal("redaction", text)
		}
	}
}

func TestAssistantProposalExpiryDismissalAndUndoConflict(t *testing.T) {
	b, s, h := testBrowser(t)
	b.login()
	installProposal := func() *assistantProposal {
		cfg, _ := s.Settings()
		var patch map[string]json.RawMessage
		_ = json.Unmarshal([]byte(`{"soarSettings.maxInflight":64}`), &patch)
		p, err := buildAssistantProposal(cfg, patch, "test")
		if err != nil || p == nil {
			t.Fatal("proposal", err)
		}
		h.assistant.mu.Lock()
		h.assistant.sessions[b.csrf] = &assistantConversation{Proposal: p, Used: time.Now()}
		h.assistant.mu.Unlock()
		return p
	}
	p := installProposal()
	p.Expires = time.Now().Add(-time.Second).UnixMilli()
	b.request("/api/assistant/apply", "POST", map[string]any{"id": p.ID, "confirm": true}, 409)
	p = installProposal()
	b.request("/api/assistant/proposal", "DELETE", map[string]any{"id": p.ID}, 200)
	b.request("/api/assistant/apply", "POST", map[string]any{"id": p.ID, "confirm": true}, 409)
	p = installProposal()
	var applied struct {
		Undo *assistantProposal `json:"undo"`
	}
	_ = json.Unmarshal(b.request("/api/assistant/apply", "POST", map[string]any{"id": p.ID, "confirm": true}, 200), &applied)
	if applied.Undo == nil {
		t.Fatal("missing undo")
	}
	cfg, _ := s.Settings()
	cfg.ServerStreamLimit = 99
	if err := s.SaveSettings(cfg); err != nil {
		t.Fatal(err)
	}
	b.request("/api/assistant/undo", "POST", map[string]any{"id": applied.Undo.ID, "confirm": true}, 409)
	got, _ := s.Settings()
	if got.ServerStreamLimit != 99 || got.profileSettings().MaxInflight != 64 {
		t.Fatal("undo overwrote concurrent changes")
	}
}
