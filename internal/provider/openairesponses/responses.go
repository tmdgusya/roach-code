// Package openairesponses implements the OpenAI Responses API provider (POST
// /responses, SSE streaming) used by Codex / GPT reasoning models. It speaks a
// different wire shape than /chat/completions — typed `input` items instead of
// `messages`, a top-level `instructions` for the system prompt, flat function
// tools, `response.*` stream events, and `call_id`-keyed function calls — so it
// is its own provider kind rather than a flag on the openai client. It
// self-registers under both "openai-responses" and "codex".
//
// Auth is an API key (Bearer) for now; ChatGPT-subscription OAuth is a planned
// follow-up. To keep that addition from touching the request path, the
// Authorization header is set in one place (setAuth).
//
// Reasoning IS round-tripped (stateless, store=false): the request sets
// include=["reasoning.encrypted_content"], the stream's reasoning items carry an
// opaque encrypted_content blob which we surface as Chunk.Signature (so the agent
// stores it in Message.ReasoningSignature, exactly like the Anthropic thinking
// signature), and buildRequest replays it as a `reasoning` input item before the
// turn's function_call items. This keeps a reasoning model's chain-of-thought
// across tool calls without server-side state. Captures the last reasoning item
// per turn (the common single-block case).
//
// Auth: API key (Bearer OPENAI_API_KEY) OR ChatGPT-subscription OAuth (see auth.go).
// The credential abstraction picks the mode and base URL; the request-building
// path never hardcodes a header.
package openairesponses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"roach-code/internal/netclient"
	"roach-code/internal/provider"
)

const defaultBaseURL = "https://api.openai.com/v1"

func init() {
	provider.Register("openai-responses", New)
	provider.Register("codex", New)
}

// New builds a Responses API provider from a resolved config.
func New(cfg provider.Config) (provider.Provider, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("openai-responses: model is required for provider %q", cfg.Name)
	}
	name := cfg.Name
	if name == "" {
		name = "codex"
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	keyEnv, _ := cfg.Extra["api_key_env"].(string)
	effort, _ := cfg.Extra["effort"].(string)
	effort = strings.ToLower(strings.TrimSpace(effort))
	switch effort {
	case "", "low", "medium", "high":
	case "max":
		effort = "high"
	case "auto":
		effort = ""
	default:
		return nil, fmt.Errorf("openai-responses: provider %q effort must be low, medium, or high", name)
	}
	httpClient, err := newHTTPClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("openai-responses: network: %w", err)
	}
	// Auth mode: explicit cfg.Extra["auth"] ("oauth"/"chatgpt" or "apikey"/"key")
	// wins; empty auto-detects (api key if present, else OAuth). OAuth mode talks
	// to the ChatGPT backend (base URL overridden); api-key mode uses the configured
	// base (api.openai.com/v1). The OAuth credential is lazy — with no stored login
	// it returns an actionable error at request time, so `doctor` still lists codex.
	authMode, _ := cfg.Extra["auth"].(string)
	useOAuth := false
	switch strings.ToLower(strings.TrimSpace(authMode)) {
	case "oauth", "chatgpt":
		useOAuth = true
	case "apikey", "api_key", "key":
		useOAuth = false
	default:
		useOAuth = cfg.APIKey == ""
	}
	var cred credential
	if useOAuth {
		cred = &oauthCred{http: httpClient, sessionID: newSessionID()}
		baseURL = chatgptBaseURL
	} else {
		cred = apiKeyCred{key: cfg.APIKey, keyEnv: keyEnv}
	}
	return &client{
		name:    name,
		keyEnv:  keyEnv,
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   cfg.Model,
		effort:  effort,
		http:    httpClient,
		cred:    cred,
	}, nil
}

func newHTTPClient(cfg provider.Config) (*http.Client, error) {
	spec, _ := cfg.Extra["proxy_spec"].(netclient.ProxySpec)
	return netclient.NewHTTPClient(spec, 0, netclient.TransportOptions{
		DialTimeout:           30 * time.Second,
		KeepAlive:             30 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second, // reasoning models can think before the first token
	})
}

type client struct {
	name    string
	keyEnv  string
	baseURL string
	model   string
	effort  string // reasoning effort: low|medium|high; "" = provider default
	http    *http.Client
	cred    credential // API-key or ChatGPT-OAuth; sets auth headers per request
}

func (c *client) Name() string { return c.name }

func (c *client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	body, err := json.Marshal(c.buildRequest(req))
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", c.name, err)
	}
	resp, err := c.sendWithRetry(ctx, body)
	if err != nil {
		return nil, err
	}
	out := make(chan provider.Chunk)
	go c.readStream(ctx, resp, out)
	return out, nil
}

func (c *client) sendWithRetry(ctx context.Context, body []byte) (*http.Response, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<(attempt-1))*500*time.Millisecond + time.Duration(rand.Intn(250))*time.Millisecond
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("%s: build request: %w", c.name, err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		if err := c.cred.apply(ctx, httpReq); err != nil {
			return nil, fmt.Errorf("%s: %w", c.name, err)
		}

		resp, err := c.http.Do(httpReq)
		if err != nil {
			if !isTransientErr(err) {
				return nil, fmt.Errorf("%s: request failed: %w", c.name, err)
			}
			lastErr = fmt.Errorf("%s: request failed: %w", c.name, err)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		msg, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if readErr != nil {
			msg = []byte(fmt.Sprintf("(could not read error body: %v)", readErr))
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, &provider.AuthError{Provider: c.name, KeyEnv: c.keyEnv, Status: resp.StatusCode}
		}
		statusErr := fmt.Errorf("%s: status %d: %s", c.name, resp.StatusCode, strings.TrimSpace(string(msg)))
		if !isRetryableStatus(resp.StatusCode) {
			return nil, statusErr
		}
		lastErr = statusErr
	}
	return nil, lastErr
}

func isRetryableStatus(s int) bool {
	return s == http.StatusRequestTimeout || s == http.StatusTooManyRequests || (s >= 500 && s <= 599)
}

func isTransientErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// buildRequest maps the transport-agnostic provider.Request onto the Responses
// wire shape: system messages become the top-level `instructions`, user/assistant
// text become typed message items, assistant tool calls become `function_call`
// items, and tool results become `function_call_output` items keyed by call_id.
func (c *client) buildRequest(req provider.Request) respRequest {
	src := provider.SanitizeToolPairing(req.Messages)

	var instructions []string
	input := make([]any, 0, len(src))
	for _, m := range src {
		switch m.Role {
		case provider.RoleSystem:
			if m.Content != "" {
				instructions = append(instructions, m.Content)
			}
		case provider.RoleUser:
			input = append(input, respMessageItem{
				Type:    "message",
				Role:    "user",
				Content: []respContent{{Type: "input_text", Text: m.Content}},
			})
		case provider.RoleAssistant:
			// Replay the encrypted reasoning item (when captured) AHEAD of this
			// turn's output, so a reasoning model keeps its chain-of-thought across
			// tool calls. The blob is carried opaquely in ReasoningSignature (set
			// from the stream's reasoning item encrypted_content) and is only
			// meaningful because buildRequest sets include=["reasoning.encrypted_content"].
			// Order matters: reasoning → message → function_calls (the stateless
			// chain shape the API accepts).
			if m.ReasoningSignature != "" {
				input = append(input, respReasoningItem{
					Type:             "reasoning",
					EncryptedContent: m.ReasoningSignature,
					Summary:          []respSummary{},
				})
			}
			// Emit visible text (if any) as a message item, then one function_call
			// item per tool call. An assistant turn that is pure tool calls has no
			// message item — an empty content array is rejected.
			if m.Content != "" {
				input = append(input, respMessageItem{
					Type:    "message",
					Role:    "assistant",
					Content: []respContent{{Type: "output_text", Text: m.Content}},
				})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				input = append(input, respFunctionCall{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Name,
					Arguments: args,
				})
			}
		case provider.RoleTool:
			input = append(input, respFunctionCallOutput{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: m.Content,
			})
		}
	}

	var tools []respTool
	for _, t := range req.Tools {
		tools = append(tools, respTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}

	out := respRequest{
		Model:             c.model,
		Instructions:      strings.Join(instructions, "\n\n"),
		Input:             input,
		Tools:             tools,
		Stream:            true,
		Store:             false,
		ParallelToolCalls: true,
		MaxOutputTokens:   req.MaxTokens,
		// Ask the API to return reasoning as an encrypted, replayable blob (rather
		// than dropping it) so we can round-trip it across tool calls while keeping
		// store=false. Harmless for non-reasoning models.
		Include: []string{"reasoning.encrypted_content"},
	}
	if c.effort != "" {
		out.Reasoning = &respReasoning{Effort: c.effort, Summary: "auto"}
	}
	return out
}

// readStream parses the Responses SSE event stream. Tool calls arrive as
// output items: `response.output_item.added` carries the call_id + name (and
// triggers ChunkToolCallStart), arguments stream via
// `response.function_call_arguments.delta`, and the call is emitted as a
// complete ChunkToolCall on `response.output_item.done`. Text and reasoning
// arrive as their own delta events. `response.completed` carries usage.
func (c *client) readStream(ctx context.Context, resp *http.Response, out chan<- provider.Chunk) {
	defer resp.Body.Close()
	defer close(out)

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			resp.Body.Close()
		case <-done:
		}
	}()

	// acc maps an output item id (e.g. "fc_…") to the tool call accumulating
	// under it. The call's id is the call_id (e.g. "call_…"), which is what
	// round-trips as ToolCall.ID / tool_call_id.
	acc := map[string]*provider.ToolCall{}
	sawToolCall := false

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var ev respEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			out <- provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("%s: decode stream: %w", c.name, err)}
			return
		}

		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta != "" {
				out <- provider.Chunk{Type: provider.ChunkText, Text: ev.Delta}
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if ev.Delta != "" {
				out <- provider.Chunk{Type: provider.ChunkReasoning, Text: ev.Delta}
			}
		case "response.output_item.added":
			if ev.Item != nil && ev.Item.Type == "function_call" {
				tc := &provider.ToolCall{ID: ev.Item.CallID, Name: ev.Item.Name, Arguments: ev.Item.Arguments}
				acc[ev.Item.ID] = tc
				sawToolCall = true
				out <- provider.Chunk{Type: provider.ChunkToolCallStart, ToolCall: &provider.ToolCall{ID: tc.ID, Name: tc.Name}}
			}
		case "response.function_call_arguments.delta":
			if tc := acc[ev.ItemID]; tc != nil {
				tc.Arguments += ev.Delta
			}
		case "response.output_item.done":
			// Capture the reasoning item's encrypted blob so the agent can store it
			// (in ReasoningSignature) and buildRequest can replay it next turn.
			if ev.Item != nil && ev.Item.Type == "reasoning" && ev.Item.EncryptedContent != "" {
				out <- provider.Chunk{Type: provider.ChunkReasoning, Signature: ev.Item.EncryptedContent}
			}
			if ev.Item != nil && ev.Item.Type == "function_call" {
				tc := acc[ev.Item.ID]
				if tc == nil {
					tc = &provider.ToolCall{ID: ev.Item.CallID, Name: ev.Item.Name}
					sawToolCall = true
				}
				// Prefer the streamed-complete arguments from the done event when
				// the deltas didn't accumulate (some gateways only send the final).
				if strings.TrimSpace(tc.Arguments) == "" && ev.Item.Arguments != "" {
					tc.Arguments = ev.Item.Arguments
				}
				if tc.ID == "" {
					tc.ID = ev.Item.CallID
				}
				delete(acc, ev.Item.ID)
				out <- provider.Chunk{Type: provider.ChunkToolCall, ToolCall: tc}
			}
		case "response.completed", "response.incomplete":
			u := normaliseUsage(ev)
			if sawToolCall {
				u.FinishReason = "tool_calls"
			} else if ev.Type == "response.incomplete" {
				u.FinishReason = "length"
			} else {
				u.FinishReason = "stop"
			}
			out <- provider.Chunk{Type: provider.ChunkUsage, Usage: u}
		case "response.failed", "error":
			msg := ev.Message
			if msg == "" && ev.Response != nil && ev.Response.Error != nil {
				msg = ev.Response.Error.Message
			}
			if msg == "" {
				msg = "stream error"
			}
			out <- provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("%s: %s", c.name, msg)}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		out <- provider.Chunk{Type: provider.ChunkError, Err: fmt.Errorf("%s: read stream: %w", c.name, err)}
		return
	}
	out <- provider.Chunk{Type: provider.ChunkDone}
}

// normaliseUsage maps the Responses usage shape (input/output tokens with cached
// + reasoning detail sub-objects) onto provider.Usage.
func normaliseUsage(ev respEvent) *provider.Usage {
	u := &provider.Usage{}
	if ev.Response == nil || ev.Response.Usage == nil {
		return u
	}
	w := ev.Response.Usage
	u.PromptTokens = w.InputTokens
	u.CompletionTokens = w.OutputTokens
	u.TotalTokens = w.TotalTokens
	if w.InputTokensDetails != nil {
		u.CacheHitTokens = w.InputTokensDetails.CachedTokens
	}
	if u.CacheHitTokens > 0 && w.InputTokens > u.CacheHitTokens {
		u.CacheMissTokens = w.InputTokens - u.CacheHitTokens
	} else {
		u.CacheMissTokens = w.InputTokens
	}
	if w.OutputTokensDetails != nil {
		u.ReasoningTokens = w.OutputTokensDetails.ReasoningTokens
	}
	return u
}

// --- Responses API wire protocol ---

type respRequest struct {
	Model             string         `json:"model"`
	Instructions      string         `json:"instructions,omitempty"`
	Input             []any          `json:"input"`
	Tools             []respTool     `json:"tools,omitempty"`
	Stream            bool           `json:"stream"`
	Store             bool           `json:"store"`
	ParallelToolCalls bool           `json:"parallel_tool_calls,omitempty"`
	MaxOutputTokens   int            `json:"max_output_tokens,omitempty"`
	Reasoning         *respReasoning `json:"reasoning,omitempty"`
	Include           []string       `json:"include,omitempty"`
}

// respReasoningItem replays a prior turn's reasoning as an input item. With
// store=false the chain-of-thought rides back in encrypted_content; summary is
// required and may be empty.
type respReasoningItem struct {
	Type             string        `json:"type"` // "reasoning"
	EncryptedContent string        `json:"encrypted_content,omitempty"`
	Summary          []respSummary `json:"summary"`
}

type respSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type respReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type respTool struct {
	Type        string          `json:"type"` // "function"
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type respMessageItem struct {
	Type    string        `json:"type"` // "message"
	Role    string        `json:"role"`
	Content []respContent `json:"content"`
}

type respContent struct {
	Type string `json:"type"` // input_text (user) | output_text (assistant)
	Text string `json:"text"`
}

type respFunctionCall struct {
	Type      string `json:"type"` // "function_call"
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type respFunctionCallOutput struct {
	Type   string `json:"type"` // "function_call_output"
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type respEvent struct {
	Type   string `json:"type"`
	Delta  string `json:"delta"`
	ItemID string `json:"item_id"`
	Item   *struct {
		Type             string `json:"type"`
		ID               string `json:"id"`
		CallID           string `json:"call_id"`
		Name             string `json:"name"`
		Arguments        string `json:"arguments"`
		EncryptedContent string `json:"encrypted_content"`
	} `json:"item"`
	Response *struct {
		Status string     `json:"status"`
		Usage  *respUsage `json:"usage"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
	Message string `json:"message"` // top-level error events
}

type respUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}
