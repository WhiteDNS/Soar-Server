package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type assistantModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Discovery uses the explicit form destination and never saves configuration or
// sends diagnostics. Stored credentials can only authenticate their exact saved
// destination, using the same rules as configuration updates.
func (h *HTTP) assistantModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var in struct {
		Provider string `json:"provider"`
		Protocol string `json:"protocol"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		ClearKey bool   `json:"clear_key"`
		Revision string `json:"revision"`
	}
	if e := decodeJSON(w, r, &in); e != nil {
		errorReply(w, 400, e)
		return
	}
	a := h.assistant
	a.mu.Lock()
	old, revision, client := a.cfg, a.revision, a.client
	a.mu.Unlock()
	if in.Revision != revision {
		errorReply(w, 409, errors.New("AI configuration changed; reload before fetching models"))
		return
	}
	c := AssistantConfig{Provider: in.Provider, Protocol: in.Protocol, BaseURL: strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"), APIKey: in.APIKey, Model: "discovery", MaxTokens: 4096}
	if c.APIKey == "" && !in.ClearKey && old.APIKey != "" {
		if c.BaseURL != old.BaseURL || c.Provider != old.Provider || c.Protocol != old.Protocol {
			errorReply(w, 400, errors.New("supply a new AI key or explicitly clear it when changing destination"))
			return
		}
		c.APIKey = old.APIKey
	}
	if in.ClearKey {
		c.APIKey = ""
	}
	if e := c.validate(); e != nil {
		errorReply(w, 400, e)
		return
	}
	select {
	case a.slots <- struct{}{}:
		defer func() { <-a.slots }()
	default:
		errorReply(w, 429, errors.New("AI requests are busy; retry shortly"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	models, e := fetchAssistantModels(ctx, client, c)
	if e != nil {
		errorReply(w, 502, e)
		return
	}
	jsonReply(w, 200, map[string]any{"models": models})
}

func fetchAssistantModels(ctx context.Context, client *http.Client, c AssistantConfig) ([]assistantModel, error) {
	models := map[string]assistantModel{}
	cursor, seen := "", map[string]bool{}
	remaining := int64(8 << 20)
	for page := 0; page < 10; page++ {
		endpoint, e := url.Parse(c.BaseURL + "/models")
		if e != nil {
			return nil, errors.New("invalid AI models endpoint")
		}
		q := endpoint.Query()
		if c.Protocol == "gemini" {
			q.Set("pageSize", "1000")
			if cursor != "" {
				q.Set("pageToken", cursor)
			}
		}
		if c.Protocol == "anthropic" {
			q.Set("limit", "1000")
		}
		if c.Protocol != "gemini" && cursor != "" {
			q.Set("after_id", cursor)
		}
		endpoint.RawQuery = q.Encode()
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if e != nil {
			return nil, errors.New("cannot prepare AI model list request")
		}
		req.Header.Set("Accept", "application/json")
		if c.APIKey != "" {
			switch c.Protocol {
			case "gemini":
				req.Header.Set("x-goog-api-key", c.APIKey)
			case "anthropic":
				req.Header.Set("x-api-key", c.APIKey)
			default:
				req.Header.Set("Authorization", "Bearer "+c.APIKey)
			}
		}
		if c.Protocol == "anthropic" {
			req.Header.Set("anthropic-version", "2023-06-01")
		}
		resp, e := client.Do(req)
		if e != nil {
			return nil, errors.New("cannot fetch AI models; check endpoint, key, DNS and TLS")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("AI model list returned HTTP %d; check API key and model-list support", resp.StatusCode)
		}
		raw, e := io.ReadAll(io.LimitReader(resp.Body, remaining+1))
		resp.Body.Close()
		if e != nil || int64(len(raw)) > remaining {
			return nil, errors.New("invalid or oversized AI model list")
		}
		remaining -= int64(len(raw))
		var result struct {
			Data []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
			Models []struct {
				Name        string   `json:"name"`
				DisplayName string   `json:"displayName"`
				Methods     []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			Next string `json:"nextPageToken"`
			More bool   `json:"has_more"`
			Last string `json:"last_id"`
		}
		if json.Unmarshal(raw, &result) != nil {
			return nil, errors.New("invalid AI model list response")
		}
		add := func(id, name string) {
			if c.APIKey != "" && strings.Contains(id, c.APIKey) {
				return
			}
			if c.APIKey != "" && strings.Contains(name, c.APIKey) {
				name = ""
			}
			candidate := c
			candidate.Model = id
			if candidate.validate() != nil || strings.TrimSpace(id) != id {
				return
			}
			if len(name) > 256 || strings.ContainsAny(name, "\r\n\x00") {
				name = ""
			}
			if name == "" {
				name = id
			}
			models[id] = assistantModel{ID: id, Name: name}
		}
		if c.Protocol == "gemini" {
			for _, model := range result.Models {
				for _, method := range model.Methods {
					if method == "generateContent" {
						add(strings.TrimPrefix(model.Name, "models/"), model.DisplayName)
						break
					}
				}
			}
			cursor = result.Next
		} else {
			for _, model := range result.Data {
				name := model.Name
				if model.DisplayName != "" {
					name = model.DisplayName
				}
				add(model.ID, name)
			}
			cursor = ""
			if result.More {
				cursor = result.Last
				if cursor == "" {
					return nil, errors.New("invalid AI model list pagination")
				}
			}
		}
		if len(models) > 2000 {
			return nil, errors.New("AI model list exceeds 2000 models")
		}
		if cursor == "" {
			if len(models) == 0 {
				return nil, errors.New("no usable AI models returned; check API key or use manual entry")
			}
			out := make([]assistantModel, 0, len(models))
			for _, model := range models {
				out = append(out, model)
			}
			sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
			return out, nil
		}
		if seen[cursor] || len(cursor) > 2048 {
			return nil, errors.New("invalid AI model list pagination")
		}
		seen[cursor] = true
	}
	return nil, errors.New("AI model list exceeds pagination limit")
}
