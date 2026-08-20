// Package ai is a provider-agnostic layer over chat completions.
//
// One interface, adapters for Anthropic, OpenAI, Google, and anything speaking the
// OpenAI-compatible shape — which is what a locally-run model behind Ollama, vLLM
// or LM Studio offers. The last of those is why the abstraction exists at all: the
// owner runs their own models, and an integration that only spoke to a hosted API
// would be useless to them.
//
// Every feature that uses this degrades to nothing when no provider is configured.
// AI here is a convenience on top of a to-do app that works without it, not a
// dependency the app has acquired.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/kristianwind/verdande/internal/safedial"
	"strings"
	"time"
)

// ErrNotConfigured is returned when no provider is set up. Callers treat it as
// "this feature is off", not as a failure.
var ErrNotConfigured = errors.New("ai: no provider is configured")

type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
	ProviderGoogle    Provider = "google"
	// ProviderCompatible is any OpenAI-shaped endpoint: Ollama, vLLM, LM Studio,
	// OpenRouter, a company gateway. Base URL and model are configured by hand.
	ProviderCompatible Provider = "compatible"
)

type Config struct {
	Provider Provider
	APIKey   string
	// BaseURL overrides the provider's default endpoint. Required for
	// ProviderCompatible and useful for a proxy in front of the others.
	BaseURL string
	Model   string
}

func (c Config) Configured() bool {
	if c.Provider == "" || c.Model == "" {
		return false
	}
	// A local model needs no key, which is exactly the case a key check would
	// break — so the requirement is per-provider rather than universal.
	if c.Provider == ProviderCompatible {
		return c.BaseURL != ""
	}
	return c.APIKey != ""
}

type Message struct {
	Role    string `json:"role"` // "user" or "assistant"
	Content string `json:"content"`
}

// Client talks to whichever provider is configured.
type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) *Client {
	return &Client{
		cfg: cfg,
		// Generous: a local model on modest hardware can take a while to produce
		// its first token, and the alternative is a timeout that only ever fires
		// for the people running their own.
		// safedial, not a plain client. `base_url` is typed into a field in the
		// interface, so it is a user-supplied address — and a server that fetches a
		// user-supplied address is a way to ask it to reach things the caller
		// cannot: the panel next door, a database on the same bridge, the cloud
		// metadata endpoint. On a homelab that is most of what is worth reaching.
		//
		// The check is on the resolved address rather than on the URL, because a
		// name answers whatever its owner says — and can answer differently the
		// second time, after a parse-time check has passed.
		http: safedial.Client(120 * time.Second),
	}
}

// Complete sends a system prompt and a conversation, and returns the reply.
func (c *Client) Complete(ctx context.Context, system string, messages []Message) (string, error) {
	if !c.cfg.Configured() {
		return "", ErrNotConfigured
	}
	switch c.cfg.Provider {
	case ProviderAnthropic:
		return c.anthropic(ctx, system, messages)
	case ProviderGoogle:
		return c.google(ctx, system, messages)
	default:
		// OpenAI and every compatible endpoint share one request shape.
		return c.openAICompatible(ctx, system, messages)
	}
}

// --- Anthropic --------------------------------------------------------------------

func (c *Client) anthropic(ctx context.Context, system string, messages []Message) (string, error) {
	base := c.cfg.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}

	body := map[string]any{
		"model":      c.cfg.Model,
		"max_tokens": 2048,
		"system":     system,
		"messages":   messages,
	}
	req, err := c.request(ctx, base+"/v1/messages", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := c.do(req, &parsed); err != nil {
		return "", err
	}

	var out strings.Builder
	for _, block := range parsed.Content {
		if block.Type == "text" {
			out.WriteString(block.Text)
		}
	}
	return out.String(), nil
}

// --- OpenAI and compatible ----------------------------------------------------------

func (c *Client) openAICompatible(ctx context.Context, system string, messages []Message) (string, error) {
	base := c.cfg.BaseURL
	if base == "" {
		base = "https://api.openai.com"
	}
	base = strings.TrimSuffix(base, "/")
	// A locally-run endpoint is usually given as ".../v1" already; appending it
	// again is the single most common way this is misconfigured.
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}

	all := append([]map[string]string{{"role": "system", "content": system}}, toOpenAI(messages)...)
	req, err := c.request(ctx, base+"/chat/completions", map[string]any{
		"model":    c.cfg.Model,
		"messages": all,
	})
	if err != nil {
		return "", err
	}
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := c.do(req, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("ai: the model returned nothing")
	}
	return parsed.Choices[0].Message.Content, nil
}

func toOpenAI(messages []Message) []map[string]string {
	out := make([]map[string]string, 0, len(messages))
	for _, m := range messages {
		out = append(out, map[string]string{"role": m.Role, "content": m.Content})
	}
	return out
}

// --- Google -------------------------------------------------------------------------

func (c *Client) google(ctx context.Context, system string, messages []Message) (string, error) {
	base := c.cfg.BaseURL
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}

	contents := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		role := "user"
		if m.Role == "assistant" {
			role = "model" // Google's name for the same thing
		}
		contents = append(contents, map[string]any{
			"role": role, "parts": []map[string]string{{"text": m.Content}},
		})
	}

	body := map[string]any{
		"contents":          contents,
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": system}}},
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", base, c.cfg.Model, c.cfg.APIKey)
	req, err := c.request(ctx, url, body)
	if err != nil {
		return "", err
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := c.do(req, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Candidates) == 0 {
		return "", fmt.Errorf("ai: the model returned nothing")
	}

	var out strings.Builder
	for _, part := range parsed.Candidates[0].Content.Parts {
		out.WriteString(part.Text)
	}
	return out.String(), nil
}

// --- plumbing ---------------------------------------------------------------------------

func (c *Client) request(ctx context.Context, url string, body any) (*http.Request, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ai: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		// The provider's own message is included: for a local model it is usually
		// the actual problem ("model not found"), and hiding it would leave the
		// operator with nothing to go on.
		// The provider's body is no longer passed back to the caller.
		//
		// It was, and combined with a free-text base URL that made a working
		// internal scanner: point it at a host, read the first three hundred bytes
		// of whatever answered. The operator still needs the message — for a local
		// model it is usually the actual problem — so it goes to the log, where it
		// is available to the person running the instance and to nobody else.
		return &upstreamError{Host: req.URL.Host, Status: resp.Status, Body: truncate(string(raw), 300)}
	}
	return json.Unmarshal(raw, out)
}

// upstreamError keeps the provider's own words for the log and away from the
// response. Error() is what reaches a caller; Detail() is what is written down.
type upstreamError struct {
	Host   string
	Status string
	Body   string
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("ai: %s said %s", e.Host, e.Status)
}

// Detail is the whole of it, for the instance's own error log.
func (e *upstreamError) Detail() string {
	return fmt.Sprintf("ai: %s said %s: %s", e.Host, e.Status, e.Body)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// --- features ------------------------------------------------------------------------

// SplitIntoSubtasks asks for a task to be broken down.
//
// The prompt insists on a bare JSON array because the result is parsed, and a model
// that helpfully wraps it in prose or a code fence produces a parse failure that
// looks to the user like the feature being broken.
func (c *Client) SplitIntoSubtasks(ctx context.Context, task, description, locale string) ([]string, error) {
	language := "Danish"
	if strings.HasPrefix(strings.ToLower(locale), "en") {
		language = "English"
	}

	system := fmt.Sprintf(`You break a task into concrete sub-tasks.

Reply with a JSON array of strings and nothing else — no prose, no code fence.
Write the sub-tasks in %s. Give between two and seven of them. Each one should be
a single concrete action somebody could do in one sitting, phrased the way the
original task is phrased. Do not restate the original task as a sub-task.`, language)

	prompt := "Task: " + task
	if description != "" {
		prompt += "\nNotes: " + description
	}

	reply, err := c.Complete(ctx, system, []Message{{Role: "user", Content: prompt}})
	if err != nil {
		return nil, err
	}

	var subtasks []string
	if err := json.Unmarshal([]byte(extractJSON(reply)), &subtasks); err != nil {
		return nil, fmt.Errorf("ai: the model did not return a list: %s", truncate(reply, 200))
	}

	out := make([]string, 0, len(subtasks))
	for _, s := range subtasks {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ai: the model returned an empty list")
	}
	return out, nil
}

// WeeklySummary produces a short prioritisation note over what is outstanding.
func (c *Client) WeeklySummary(ctx context.Context, tasks []string, locale string) (string, error) {
	language := "Danish"
	if strings.HasPrefix(strings.ToLower(locale), "en") {
		language = "English"
	}

	system := fmt.Sprintf(`You help somebody decide what to do this week.

Write in %s. Be brief — at most six short lines. Say what looks most urgent and
why, and name anything that appears to be slipping. Do not restate the whole list
back, do not congratulate, and do not offer productivity advice.`, language)

	prompt := "These are the open tasks:\n" + strings.Join(tasks, "\n")
	return c.Complete(ctx, system, []Message{{Role: "user", Content: prompt}})
}

// extractJSON pulls an array out of a reply that may have been wrapped in a code
// fence or preceded by a sentence, which models do however firmly they are asked
// not to.
func extractJSON(reply string) string {
	reply = strings.TrimSpace(reply)

	if fence := strings.Index(reply, "```"); fence >= 0 {
		rest := reply[fence+3:]
		if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
			rest = rest[newline+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			reply = strings.TrimSpace(rest[:end])
		}
	}

	start := strings.IndexByte(reply, '[')
	end := strings.LastIndexByte(reply, ']')
	if start >= 0 && end > start {
		return reply[start : end+1]
	}
	return reply
}
