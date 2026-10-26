package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAssistantModelDiscoveryProtocolsAndPagination(t *testing.T) {
	for _, protocol := range []string{"openai", "gemini", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				page := calls.Add(1)
				if r.Method != "GET" || r.URL.Path != "/v1/models" || r.URL.Query().Has("key") {
					t.Error("invalid discovery request")
				}
				switch protocol {
				case "gemini":
					if r.Header.Get("x-goog-api-key") != "private-model-key" {
						t.Error("missing Gemini key")
					}
					if page == 1 {
						jsonReply(w, 200, map[string]any{"models": []any{map[string]any{"name": "models/z-model", "displayName": "Z model", "supportedGenerationMethods": []string{"generateContent"}}, map[string]any{"name": "models/embedding-only", "supportedGenerationMethods": []string{"embedContent"}}}, "nextPageToken": "next"})
						return
					}
					if r.URL.Query().Get("pageToken") != "next" {
						t.Error("missing Gemini cursor")
					}
					jsonReply(w, 200, map[string]any{"models": []any{map[string]any{"name": "models/a-model", "supportedGenerationMethods": []string{"generateContent"}}}})
				default:
					if protocol == "anthropic" {
						if r.Header.Get("x-api-key") != "private-model-key" || r.Header.Get("anthropic-version") == "" {
							t.Error("missing Anthropic authentication")
						}
					} else if r.Header.Get("Authorization") != "Bearer private-model-key" {
						t.Error("missing compatible authentication")
					}
					if page == 1 {
						jsonReply(w, 200, map[string]any{"data": []any{map[string]string{"id": "z-model", "display_name": "Z model"}}, "has_more": true, "last_id": "z-model"})
						return
					}
					if r.URL.Query().Get("after_id") != "z-model" {
						t.Error("missing cursor")
					}
					jsonReply(w, 200, map[string]any{"data": []any{map[string]string{"id": "a-model"}, map[string]string{"id": "z-model", "display_name": "Z model"}, map[string]string{"id": "invalid\nmodel"}, map[string]string{"id": "private-model-key"}}})
				}
			}))
			defer server.Close()
			c := AssistantConfig{Provider: "custom", Protocol: protocol, BaseURL: server.URL + "/v1", APIKey: "private-model-key", MaxTokens: 4096}
			models, e := fetchAssistantModels(context.Background(), newAssistantClient(), c)
			if e != nil || len(models) != 2 || models[0].ID != "a-model" || models[1].ID != "z-model" || models[1].Name != "Z model" || calls.Load() != 2 {
				t.Fatal(models, e, calls.Load())
			}
		})
	}
}

func TestAssistantModelDiscoveryAuthenticationAndStoredKeyIsolation(t *testing.T) {
	b, _, h := testBrowser(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-private-ai-key" {
			t.Error("stored key not reused")
		}
		jsonReply(w, 200, map[string]any{"data": []any{map[string]string{"id": "available-model", "name": "echo test-private-ai-key"}}})
	}))
	defer server.Close()
	input := map[string]any{"provider": "custom", "protocol": "openai", "base_url": server.URL, "revision": "invalid"}
	b.request("/api/assistant/models", "POST", input, 401)
	b.login()
	view := configureTestAssistant(t, b, server.URL, "openai")
	input["revision"] = view["revision"]
	before := h.assistant.cfg
	csrf := b.csrf
	b.csrf = "invalid"
	b.request("/api/assistant/models", "POST", input, 403)
	b.csrf = csrf
	if calls.Load() != 0 {
		t.Fatal("unauthorized request reached provider")
	}
	raw := b.request("/api/assistant/models", "POST", input, 200)
	if bytes.Contains(raw, []byte("test-private-ai-key")) {
		t.Fatal("provider echo exposed saved key")
	}
	if !reflect.DeepEqual(before, h.assistant.cfg) {
		t.Fatal("discovery saved configuration")
	}
	input["base_url"] = server.URL + "/changed"
	b.request("/api/assistant/models", "POST", input, 400)
	input["base_url"] = server.URL
	input["revision"] = "stale"
	b.request("/api/assistant/models", "POST", input, 409)
	if calls.Load() != 1 {
		t.Fatal("stale or changed destination reached provider")
	}
	b.request("/api/assistant/models", "GET", nil, 405)
}

func TestAssistantModelDiscoveryUnsavedDestinationAndErrors(t *testing.T) {
	b, _, h := testBrowser(t)
	b.login()
	var mode atomic.Value
	mode.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load().(string) {
		case "http":
			w.WriteHeader(401)
			_, _ = w.Write([]byte("secret-provider-error"))
		case "json":
			_, _ = w.Write([]byte("invalid"))
		case "empty":
			jsonReply(w, 200, map[string]any{"data": []any{}})
		case "cycle":
			jsonReply(w, 200, map[string]any{"data": []any{map[string]string{"id": "test"}}, "has_more": true, "last_id": "same"})
		default:
			jsonReply(w, 200, map[string]any{"data": []any{map[string]string{"id": "first-model"}}})
		}
	}))
	defer server.Close()
	var view map[string]any
	_ = json.Unmarshal(b.request("/api/assistant/config", "GET", nil, 200), &view)
	input := map[string]any{"provider": "custom", "protocol": "openai", "base_url": server.URL, "api_key": "new-unsaved-key", "revision": view["revision"]}
	b.request("/api/assistant/models", "POST", input, 200)
	if h.assistant.cfg.Model != "" || h.assistant.cfg.APIKey != "" {
		t.Fatal("discovery persisted unsaved settings")
	}
	for _, failure := range []string{"http", "json", "empty", "cycle"} {
		mode.Store(failure)
		raw := b.request("/api/assistant/models", "POST", input, 502)
		if strings.Contains(string(raw), "secret-provider-error") || strings.Contains(string(raw), "new-unsaved-key") {
			t.Fatal("provider error leaked private details")
		}
	}
	input["base_url"] = "https://169.254.169.254"
	b.request("/api/assistant/models", "POST", input, 400)
}
