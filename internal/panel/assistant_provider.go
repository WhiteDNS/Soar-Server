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
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type assistantProvider struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	BaseURL  string `json:"base_url"`
}

// Presets configure a protocol, never an account or a silently chosen model.
var assistantProviders = []assistantProvider{
	{"gemini", "Google Gemini", "gemini", "https://generativelanguage.googleapis.com/v1beta"},
	{"openrouter", "OpenRouter", "openai", "https://openrouter.ai/api/v1"},
	{"9router", "9Router", "openai", "http://127.0.0.1:20128/v1"},
	{"deepseek", "DeepSeek", "openai", "https://api.deepseek.com"},
	{"openai", "OpenAI", "openai", "https://api.openai.com/v1"},
	{"anthropic", "Anthropic / Claude", "anthropic", "https://api.anthropic.com/v1"},
	{"groq", "Groq", "openai", "https://api.groq.com/openai/v1"},
	{"mistral", "Mistral", "openai", "https://api.mistral.ai/v1"},
	{"xai", "xAI / Grok", "openai", "https://api.x.ai/v1"},
	{"together", "Together AI", "openai", "https://api.together.xyz/v1"},
	{"fireworks", "Fireworks AI", "openai", "https://api.fireworks.ai/inference/v1"},
	{"ollama", "Ollama (local)", "openai", "http://127.0.0.1:11434/v1"},
	{"lmstudio", "LM Studio (local)", "openai", "http://127.0.0.1:1234/v1"},
	{"custom", "Custom", "openai", ""},
}

type AssistantConfig struct {
	Enabled   bool   `json:"enabled"`
	Provider  string `json:"provider"`
	Protocol  string `json:"protocol"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key,omitempty"`
	JSONMode  bool   `json:"json_mode"`
	MaxTokens int    `json:"max_tokens"`
}

func (c *AssistantConfig) validate() error {
	found := false
	for _, p := range assistantProviders {
		if c.Provider == p.ID {
			found = true
			break
		}
	}
	if !found {
		return errors.New("invalid AI provider")
	}
	if c.Protocol != "openai" && c.Protocol != "gemini" && c.Protocol != "anthropic" {
		return errors.New("invalid AI protocol")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || strings.Contains(u.Path, "..") || len(c.BaseURL) > 512 {
		return errors.New("invalid AI base URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && assistantLoopback(u.Hostname())) {
		return errors.New("AI endpoint requires HTTPS; HTTP is allowed only on loopback")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !assistantPublicIP(ip) && !ip.IsLoopback() {
		return errors.New("AI endpoint must be public or loopback")
	}
	if strings.TrimSpace(c.Model) == "" || len(c.Model) > 160 || strings.ContainsAny(c.Model, "\r\n\t?#") || strings.HasPrefix(c.Model, "/") {
		return errors.New("invalid AI model")
	}
	if c.Protocol == "gemini" && strings.ContainsAny(c.Model, "/:") {
		return errors.New("use a Gemini model ID without models/ prefix")
	}
	if len(c.APIKey) > 4096 || strings.ContainsAny(c.APIKey, "\r\n") {
		return errors.New("invalid AI key")
	}
	if c.MaxTokens < 256 || c.MaxTokens > 16384 {
		return errors.New("AI max tokens must be between 256 and 16384")
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	return nil
}

func assistantLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
func assistantPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}

func newAssistantClient() *http.Client {
	t := &http.Transport{TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 90 * time.Second, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, errors.New("AI endpoint has no addresses")
		}
		for _, ip := range ips {
			if !(assistantPublicIP(ip) || assistantLoopback(host) && ip.IsLoopback()) {
				return nil, errors.New("AI endpoint resolved to a blocked address")
			}
		}
		var last error
		for _, ip := range ips {
			conn, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}
	return &http.Client{Transport: t, Timeout: 100 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("AI redirects are disabled") }}
}

type assistantMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Called only after the administrator explicitly approves this exact destination
// in the chat UI. Only the sanitized context is passed as message content. API
// credentials authenticate the chosen endpoint; they never enter the prompt.
func callAssistant(ctx context.Context, client *http.Client, c AssistantConfig, confirmedEndpoint, system string, messages []assistantMessage) (string, error) {
	if confirmedEndpoint != c.BaseURL {
		return "", errors.New("confirm the current AI destination before sending")
	}
	endpoint := c.BaseURL
	var payload any
	switch c.Protocol {
	case "gemini":
		contents := []any{}
		for _, m := range messages {
			role := m.Role
			if role == "assistant" {
				role = "model"
			}
			contents = append(contents, map[string]any{"role": role, "parts": []any{map[string]string{"text": m.Content}}})
		}
		generation := map[string]any{"maxOutputTokens": c.MaxTokens}
		if c.JSONMode {
			generation["responseMimeType"] = "application/json"
		}
		payload = map[string]any{"systemInstruction": map[string]any{"parts": []any{map[string]string{"text": system}}}, "contents": contents, "generationConfig": generation}
		endpoint += "/models/" + url.PathEscape(c.Model) + ":generateContent"
	case "anthropic":
		payload = map[string]any{"model": c.Model, "system": system, "messages": messages, "max_tokens": c.MaxTokens}
		endpoint += "/messages"
	default:
		all := append([]assistantMessage{{Role: "system", Content: system}}, messages...)
		body := map[string]any{"model": c.Model, "messages": all, "stream": false}
		if c.Provider == "openai" {
			body["max_completion_tokens"] = c.MaxTokens
			body["store"] = false
		} else {
			body["max_tokens"] = c.MaxTokens
		}
		if c.JSONMode {
			body["response_format"] = map[string]string{"type": "json_object"}
		}
		payload = body
		endpoint += "/chat/completions"
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("invalid AI request")
	}
	req.Header.Set("Content-Type", "application/json")
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
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", errors.New("AI request cancelled or timed out")
		}
		return "", errors.New("AI connection failed; check endpoint, DNS and TLS")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("AI provider returned HTTP %d", resp.StatusCode)
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 {
		return "", errors.New("invalid or oversized AI response")
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Finish string `json:"finish_reason"`
		} `json:"choices"`
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
			Finish string `json:"finishReason"`
		} `json:"candidates"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Stop string `json:"stop_reason"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return "", errors.New("invalid AI response envelope")
	}
	var answer strings.Builder
	switch c.Protocol {
	case "gemini":
		if len(result.Candidates) > 0 {
			v := result.Candidates[0]
			if v.Finish != "" && v.Finish != "STOP" {
				return "", errors.New("AI response blocked or incomplete")
			}
			for _, p := range v.Content.Parts {
				if !p.Thought {
					answer.WriteString(p.Text)
				}
			}
		}
	case "anthropic":
		if result.Stop != "" && result.Stop != "end_turn" {
			return "", errors.New("AI response incomplete")
		}
		for _, p := range result.Content {
			if p.Type == "text" {
				answer.WriteString(p.Text)
			}
		}
	default:
		if len(result.Choices) > 0 {
			v := result.Choices[0]
			if v.Finish != "" && v.Finish != "stop" {
				return "", errors.New("AI response incomplete")
			}
			answer.WriteString(v.Message.Content)
		}
	}
	if answer.Len() == 0 || answer.Len() > 48000 {
		return "", errors.New("AI response is empty or too long")
	}
	return answer.String(), nil
}

var assistantSecrets = regexp.MustCompile(`(?i)(?:sk-[a-z0-9_-]{8,}|AIza[a-z0-9_-]{12,}|(?:api[_-]?key|password|token|secret|private[_-]?key|authorization)["']?\s*[=:]\s*["']?[^\s,;"'}]+|[a-z][a-z0-9+.-]*://[^\s<>]+)`)

func assistantRedact(text string, c AssistantConfig) string {
	if c.APIKey != "" {
		text = strings.ReplaceAll(text, c.APIKey, "[redacted]")
	}
	return assistantSecrets.ReplaceAllString(text, "[redacted]")
}
