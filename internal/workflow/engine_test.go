package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"roach-code/internal/provider"
	"roach-code/internal/tool"
)

// fakeProvider answers each sub-agent turn with reply(lastUserPrompt) and no
// tool calls, so a workflow agent's final answer is deterministic.
type fakeProvider struct {
	reply            func(prompt string) string
	completionTokens int
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	var prompt string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == provider.RoleUser {
			prompt = req.Messages[i].Content
			break
		}
	}
	ch := make(chan provider.Chunk, 4)
	ch <- provider.Chunk{Type: provider.ChunkText, Text: f.reply(prompt)}
	if f.completionTokens > 0 {
		ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{
			CompletionTokens: f.completionTokens,
			TotalTokens:      f.completionTokens,
		}}
	}
	ch <- provider.Chunk{Type: provider.ChunkDone}
	close(ch)
	return ch, nil
}

func testEnv(reply func(string) string, tokens int) Env {
	return Env{
		Prov:      &fakeProvider{reply: reply, completionTokens: tokens},
		ParentReg: tool.NewRegistry(),
		MaxSteps:  3,
	}
}

func run(t *testing.T, env Env, caps Caps, args json.RawMessage, script string) (*Engine, string, error) {
	t.Helper()
	eng := NewEngine(env, nil, caps, args, "test")
	out, err := eng.Run(context.Background(), script)
	return eng, out, err
}

func TestEngineParallel(t *testing.T) {
	env := testEnv(func(p string) string { return strings.TrimPrefix(p, "echo ") }, 0)
	script := `
		const rs = await parallel([1, 2, 3].map(n => () => agent("echo " + n)));
		return rs.join("|");
	`
	eng, out, err := run(t, env, Caps{}, nil, script)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "1|2|3" {
		t.Fatalf("got %q want %q (Promise.all must preserve order)", out, "1|2|3")
	}
	if n := eng.spawned.Load(); n != 3 {
		t.Fatalf("spawned = %d, want 3", n)
	}
}

func TestEngineSequentialDependency(t *testing.T) {
	// b depends on a purely through `await` — this is how the "DAG" is expressed.
	env := testEnv(func(p string) string { return p }, 0)
	script := `
		const a = await agent("A");
		const b = await agent("wrap(" + a + ")");
		return b;
	`
	_, out, err := run(t, env, Caps{}, nil, script)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "wrap(A)" {
		t.Fatalf("got %q want %q", out, "wrap(A)")
	}
}

func TestEngineArgsInjection(t *testing.T) {
	env := testEnv(func(p string) string { return p }, 0)
	_, out, err := run(t, env, Caps{}, json.RawMessage(`{"x": 21}`), `return String(args.x * 2);`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "42" {
		t.Fatalf("got %q want %q", out, "42")
	}
}

func TestEngineReturnObjectIsJSON(t *testing.T) {
	env := testEnv(func(p string) string { return p }, 0)
	_, out, err := run(t, env, Caps{}, nil, `return {a: 1, b: [2, 3]};`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != `{"a":1,"b":[2,3]}` {
		t.Fatalf("got %q want JSON-stringified object", out)
	}
}

func TestEngineMaxAgentsCap(t *testing.T) {
	env := testEnv(func(p string) string { return p }, 0)
	script := `
		await agent("a");
		await agent("b");
		await agent("c");
		return "done";
	`
	eng, _, err := run(t, env, Caps{MaxAgents: 2}, nil, script)
	if err == nil {
		t.Fatal("expected an error when exceeding max_agents")
	}
	if !strings.Contains(err.Error(), "max_agents") {
		t.Fatalf("error = %v, want it to mention max_agents", err)
	}
	// The rejected third call rolled its reservation back, so the counter reflects
	// only the two that actually held a slot.
	if n := eng.spawned.Load(); n != 2 {
		t.Fatalf("spawned = %d, want 2 (rejected call must roll back)", n)
	}
}

func TestEngineMaxTokensCap(t *testing.T) {
	env := testEnv(func(p string) string { return p }, 100) // 100 output tokens per agent
	script := `
		await agent("a");
		await agent("b");
		await agent("c");
		return "done";
	`
	_, _, err := run(t, env, Caps{MaxTokens: 150}, nil, script)
	if err == nil {
		t.Fatal("expected an error when exceeding max_tokens")
	}
	if !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("error = %v, want it to mention max_tokens", err)
	}
}

func TestEngineScriptSyntaxError(t *testing.T) {
	env := testEnv(func(p string) string { return p }, 0)
	_, _, err := run(t, env, Caps{}, nil, `this is not ) valid javascript (`)
	if err == nil {
		t.Fatal("expected a syntax error to surface")
	}
}

func TestEngineLogDoesNotBreakReturn(t *testing.T) {
	env := testEnv(func(p string) string { return p }, 0)
	_, out, err := run(t, env, Caps{}, nil, `log("hello"); phase("p1"); return "ok";`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "ok" {
		t.Fatalf("got %q want %q", out, "ok")
	}
}
