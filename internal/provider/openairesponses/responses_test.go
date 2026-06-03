package openairesponses

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"roach-code/internal/provider"
)

func TestNewEffortValidation(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: ""},
		{in: "low", want: "low"},
		{in: "MEDIUM", want: "medium"},
		{in: "high", want: "high"},
		{in: "max", want: "high"}, // clamped
		{in: "auto", want: ""},
		{in: "bogus", wantErr: true},
	} {
		p, err := New(provider.Config{Name: "codex", Model: "gpt-5-codex", Extra: map[string]any{"effort": tc.in}})
		if tc.wantErr {
			if err == nil {
				t.Errorf("effort %q: want error, got nil", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("effort %q: %v", tc.in, err)
		}
		if got := p.(*client).effort; got != tc.want {
			t.Errorf("effort %q: stored %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestBuildRequestMapping covers the heart of the Responses adapter: system →
// instructions, user/assistant text → typed message items, and tools → flat
// function defs.
func TestBuildRequestMapping(t *testing.T) {
	c := &client{model: "gpt-5-codex", effort: "medium"}
	req := c.buildRequest(provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: "You are helpful."},
			{Role: provider.RoleUser, Content: "hi"},
			{Role: provider.RoleAssistant, Content: "hello there"},
		},
		Tools: []provider.ToolSchema{{Name: "ls", Description: "list", Parameters: []byte(`{"type":"object"}`)}},
	})

	if req.Instructions != "You are helpful." {
		t.Errorf("instructions = %q", req.Instructions)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "medium" {
		t.Errorf("reasoning = %+v, want effort medium", req.Reasoning)
	}
	if req.Store {
		t.Error("store must be false")
	}
	if len(req.Input) != 2 {
		t.Fatalf("input items = %d, want 2 (user + assistant)", len(req.Input))
	}
	um, ok := req.Input[0].(respMessageItem)
	if !ok || um.Role != "user" || len(um.Content) != 1 || um.Content[0].Type != "input_text" || um.Content[0].Text != "hi" {
		t.Errorf("user item = %+v", req.Input[0])
	}
	am, ok := req.Input[1].(respMessageItem)
	if !ok || am.Role != "assistant" || am.Content[0].Type != "output_text" {
		t.Errorf("assistant item = %+v", req.Input[1])
	}
	if len(req.Tools) != 1 || req.Tools[0].Type != "function" || req.Tools[0].Name != "ls" {
		t.Errorf("tools = %+v", req.Tools)
	}
}

func TestBuildRequestMapsUserImageParts(t *testing.T) {
	c := &client{model: "gpt-5-codex"}
	req := c.buildRequest(provider.Request{Messages: []provider.Message{{
		Role:    provider.RoleUser,
		Content: "describe this image",
		Parts: []provider.ContentPart{
			{Type: "text", Text: "describe this image"},
			{Type: "image", ImageURL: "data:image/png;base64,AAA"},
		},
	}}})

	if len(req.Input) != 1 {
		t.Fatalf("input items = %d, want 1", len(req.Input))
	}
	um, ok := req.Input[0].(respMessageItem)
	if !ok {
		t.Fatalf("user item type = %T", req.Input[0])
	}
	if len(um.Content) != 2 {
		t.Fatalf("content parts = %+v, want text + image", um.Content)
	}
	if um.Content[0].Type != "input_text" || um.Content[0].Text != "describe this image" {
		t.Errorf("text part = %+v", um.Content[0])
	}
	if um.Content[1].Type != "input_image" || um.Content[1].ImageURL != "data:image/png;base64,AAA" {
		t.Errorf("image part = %+v", um.Content[1])
	}
}

// TestBuildRequestToolCallRoundTrip is the highest-risk path: a multi-tool
// assistant turn followed by results must survive SanitizeToolPairing and map
// to function_call / function_call_output items whose call_ids match, so the
// API can pair each result to its call.
func TestBuildRequestToolCallRoundTrip(t *testing.T) {
	c := &client{model: "gpt-5-codex"}
	req := c.buildRequest(provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "do it"},
			{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
				{ID: "call_a", Name: "read", Arguments: `{"p":"x"}`},
				{ID: "call_b", Name: "write", Arguments: `{"p":"y"}`},
			}},
			{Role: provider.RoleTool, ToolCallID: "call_a", Name: "read", Content: "contents-x"},
			{Role: provider.RoleTool, ToolCallID: "call_b", Name: "write", Content: "ok-y"},
		},
	})

	// Expect: user message, function_call(call_a), function_call(call_b),
	// function_call_output(call_a), function_call_output(call_b).
	var calls []respFunctionCall
	var outs []respFunctionCallOutput
	for _, it := range req.Input {
		switch v := it.(type) {
		case respFunctionCall:
			calls = append(calls, v)
		case respFunctionCallOutput:
			outs = append(outs, v)
		}
	}
	if len(calls) != 2 || calls[0].CallID != "call_a" || calls[1].CallID != "call_b" {
		t.Fatalf("function_call items = %+v", calls)
	}
	if calls[0].Type != "function_call" || calls[0].Name != "read" || calls[0].Arguments != `{"p":"x"}` {
		t.Errorf("call_a = %+v", calls[0])
	}
	if len(outs) != 2 || outs[0].CallID != "call_a" || outs[1].CallID != "call_b" {
		t.Fatalf("function_call_output items = %+v", outs)
	}
	if outs[0].Type != "function_call_output" || outs[0].Output != "contents-x" {
		t.Errorf("out call_a = %+v", outs[0])
	}
}

// TestBuildRequestEmptyToolArgs ensures a tool call with no arguments serializes
// "{}" rather than "" (the API rejects empty arguments on a function_call).
func TestBuildRequestEmptyToolArgs(t *testing.T) {
	c := &client{model: "m"}
	req := c.buildRequest(provider.Request{Messages: []provider.Message{
		{Role: provider.RoleUser, Content: "go"},
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{{ID: "call_z", Name: "now"}}},
		{Role: provider.RoleTool, ToolCallID: "call_z", Content: "done"},
	}})
	for _, it := range req.Input {
		if fc, ok := it.(respFunctionCall); ok && fc.Arguments != "{}" {
			t.Errorf("empty args serialized as %q, want {}", fc.Arguments)
		}
	}
}

// TestBuildRequestReasoningRoundTrip checks that a captured reasoning blob is
// replayed as a reasoning input item BEFORE the turn's function_call items, and
// that the request opts into encrypted reasoning via include.
func TestBuildRequestReasoningRoundTrip(t *testing.T) {
	c := &client{model: "gpt-5-codex"}
	req := c.buildRequest(provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: "go"},
			{Role: provider.RoleAssistant, ReasoningSignature: "ENC_BLOB", ToolCalls: []provider.ToolCall{
				{ID: "call_a", Name: "read", Arguments: `{}`},
			}},
			{Role: provider.RoleTool, ToolCallID: "call_a", Content: "x"},
		},
	})

	foundInclude := false
	for _, inc := range req.Include {
		if inc == "reasoning.encrypted_content" {
			foundInclude = true
		}
	}
	if !foundInclude {
		t.Errorf("include = %v, want reasoning.encrypted_content", req.Include)
	}

	// Order within the input: reasoning item must precede the function_call.
	reasonIdx, callIdx := -1, -1
	for i, it := range req.Input {
		switch v := it.(type) {
		case respReasoningItem:
			reasonIdx = i
			if v.EncryptedContent != "ENC_BLOB" {
				t.Errorf("reasoning encrypted_content = %q, want ENC_BLOB", v.EncryptedContent)
			}
		case respFunctionCall:
			if callIdx == -1 {
				callIdx = i
			}
		}
	}
	if reasonIdx == -1 {
		t.Fatal("no reasoning item replayed")
	}
	if callIdx == -1 || reasonIdx > callIdx {
		t.Errorf("reasoning item idx %d must precede function_call idx %d", reasonIdx, callIdx)
	}
}

const sseReasoningStream = `data: {"type":"response.reasoning_summary_text.delta","delta":"thinking"}

data: {"type":"response.output_item.done","item":{"type":"reasoning","id":"rs_1","encrypted_content":"ENC123"}}

data: {"type":"response.output_text.delta","delta":"done"}

data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}

`

// TestReadStreamCapturesReasoningSignature verifies the reasoning item's
// encrypted_content surfaces as a ChunkReasoning Signature (the field the agent
// stores in Message.ReasoningSignature for round-tripping).
func TestReadStreamCapturesReasoningSignature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseReasoningStream))
	}))
	defer srv.Close()

	p, _ := New(provider.Config{Name: "codex", Model: "gpt-5-codex", BaseURL: srv.URL, APIKey: "k"})
	ch, err := p.Stream(context.Background(), provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "go"}}})
	if err != nil {
		t.Fatal(err)
	}
	var sig string
	for chunk := range ch {
		if chunk.Type == provider.ChunkReasoning && chunk.Signature != "" {
			sig = chunk.Signature
		}
		if chunk.Type == provider.ChunkError {
			t.Fatalf("error: %v", chunk.Err)
		}
	}
	if sig != "ENC123" {
		t.Errorf("captured reasoning signature = %q, want ENC123", sig)
	}
}

const sseStream = `data: {"type":"response.created","response":{"status":"in_progress"}}

data: {"type":"response.reasoning_summary_text.delta","delta":"let me think"}

data: {"type":"response.output_text.delta","delta":"Hello "}

data: {"type":"response.output_text.delta","delta":"world"}

data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_abc","name":"read_file"}}

data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"path\":"}

data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"\"a.txt\"}"}

data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_abc","name":"read_file","arguments":"{\"path\":\"a.txt\"}"}}

data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":40},"output_tokens_details":{"reasoning_tokens":8}}}}

`

func TestReadStreamParsesEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseStream))
	}))
	defer srv.Close()

	p, err := New(provider.Config{Name: "codex", Model: "gpt-5-codex", BaseURL: srv.URL, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := p.Stream(context.Background(), provider.Request{Messages: []provider.Message{{Role: provider.RoleUser, Content: "go"}}})
	if err != nil {
		t.Fatal(err)
	}

	var text, reasoning string
	var startSeen bool
	var fullCall *provider.ToolCall
	var usage *provider.Usage
	var done bool
	for ch := range ch {
		switch ch.Type {
		case provider.ChunkText:
			text += ch.Text
		case provider.ChunkReasoning:
			reasoning += ch.Text
		case provider.ChunkToolCallStart:
			startSeen = true
			if ch.ToolCall.ID != "call_abc" || ch.ToolCall.Name != "read_file" {
				t.Errorf("start = %+v", ch.ToolCall)
			}
		case provider.ChunkToolCall:
			fullCall = ch.ToolCall
		case provider.ChunkUsage:
			usage = ch.Usage
		case provider.ChunkDone:
			done = true
		case provider.ChunkError:
			t.Fatalf("unexpected error chunk: %v", ch.Err)
		}
	}

	if text != "Hello world" {
		t.Errorf("text = %q", text)
	}
	if reasoning != "let me think" {
		t.Errorf("reasoning = %q", reasoning)
	}
	if !startSeen {
		t.Error("no ChunkToolCallStart")
	}
	if fullCall == nil || fullCall.ID != "call_abc" || fullCall.Name != "read_file" || fullCall.Arguments != `{"path":"a.txt"}` {
		t.Errorf("tool call = %+v", fullCall)
	}
	if usage == nil {
		t.Fatal("no usage")
	}
	if usage.PromptTokens != 100 || usage.CompletionTokens != 20 || usage.CacheHitTokens != 40 || usage.CacheMissTokens != 60 || usage.ReasoningTokens != 8 {
		t.Errorf("usage = %+v", usage)
	}
	if usage.FinishReason != "tool_calls" {
		t.Errorf("finish = %q, want tool_calls", usage.FinishReason)
	}
	if !done {
		t.Error("no ChunkDone")
	}
}
