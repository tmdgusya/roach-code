package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dop251/goja"

	"roach-code/internal/agent"
	"roach-code/internal/event"
)

// jsAgent implements the native __agent(optsJSON) -> Promise. It is invoked on
// the loop goroutine; the actual sub-agent runs on a worker goroutine and the
// promise is resolved back onto the loop (goja's runtime is not goroutine-safe).
func (e *Engine) jsAgent(call goja.FunctionCall) goja.Value {
	optsJSON := call.Argument(0).String()
	p, resolve, reject := e.vm.NewPromise()

	go func() {
		out, err := e.runOneAgent(e.ctx, optsJSON)
		e.loop.RunOnLoop(func(vm *goja.Runtime) {
			if err != nil {
				reject(vm.ToValue(err.Error()))
				return
			}
			resolve(vm.ToValue(out))
		})
	}()

	return e.vm.ToValue(p)
}

// runOneAgent runs a single workflow agent: enforce caps, acquire a concurrency
// slot, surface it as a subagent-panel row, then delegate to the existing
// agent.RunSubAgent. Runs on a worker goroutine.
func (e *Engine) runOneAgent(ctx context.Context, optsJSON string) (string, error) {
	var opts struct {
		Prompt string   `json:"prompt"`
		Label  string   `json:"label"`
		Tools  []string `json:"tools"`
	}
	if err := json.Unmarshal([]byte(optsJSON), &opts); err != nil {
		return "", fmt.Errorf("agent(): invalid options: %w", err)
	}
	if strings.TrimSpace(opts.Prompt) == "" {
		return "", errors.New("agent(): prompt is required")
	}

	// Agent-count cap. Increment first, roll back on rejection, so concurrent
	// callers can't slip past the ceiling.
	if n := e.spawned.Add(1); n > int64(e.caps.MaxAgents) {
		e.spawned.Add(-1)
		return "", fmt.Errorf("agent(): run exceeded max_agents (%d)", e.caps.MaxAgents)
	}

	// Token ceiling. Best-effort pre-check — tokens accrue asynchronously as
	// agents stream, so this bounds runaway growth without being exact.
	if e.caps.MaxTokens > 0 && e.tokens.Load() >= e.caps.MaxTokens {
		return "", fmt.Errorf("agent(): run exceeded max_tokens (%d)", e.caps.MaxTokens)
	}

	// Concurrency slot.
	select {
	case e.sema <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-e.sema }()

	agentID := fmt.Sprintf("wf-%s-%d", e.runID, e.seq.Add(1))
	label := opts.Label
	if label == "" {
		label = firstWords(opts.Prompt, 8)
	}

	e.emitDispatch(agentID, label, opts.Prompt)

	// Workflow agents inherit the parent tool set minus the meta-tools (task,
	// skills) and run_workflow itself, so delegation stays one layer deep and a
	// workflow can't recursively spawn workflows.
	reg := agent.FilterRegistry(e.env.ParentReg, opts.Tools,
		append(agent.SubagentMetaTools(), "run_workflow")...)

	out, err := agent.RunSubAgent(ctx, e.env.Prov, reg, e.env.SysPrompt, opts.Prompt, agent.Options{
		MaxSteps:      e.env.MaxSteps,
		Temperature:   e.env.Temperature,
		Pricing:       e.env.Pricing,
		Gate:          e.env.Gate,
		ContextWindow: e.env.ContextWindow,
		ArchiveDir:    e.env.ArchiveDir,
	}, e.nestSink(agentID))

	e.emitResult(agentID, out, err)
	if err != nil {
		return "", err
	}
	return out, nil
}

// jsLog implements log(msg): a user-visible workflow progress line.
func (e *Engine) jsLog(call goja.FunctionCall) goja.Value {
	e.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "workflow: " + call.Argument(0).String()})
	return goja.Undefined()
}

// jsPhase implements phase(title): a labelled progress boundary (v1 surfaces it
// as a notice; a dedicated panel grouping is a later phase).
func (e *Engine) jsPhase(call goja.FunctionCall) goja.Value {
	e.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "workflow phase: " + call.Argument(0).String()})
	return goja.Undefined()
}

// jsBudget implements __budget(): a fresh snapshot {total, spent, remaining,
// agents, maxAgents}. total/remaining are null when no token ceiling is set.
// Runs on the loop goroutine.
func (e *Engine) jsBudget(goja.FunctionCall) goja.Value {
	obj := e.vm.NewObject()
	spent := e.tokens.Load()
	_ = obj.Set("spent", spent)
	_ = obj.Set("agents", e.spawned.Load())
	_ = obj.Set("maxAgents", int64(e.caps.MaxAgents))
	if e.caps.MaxTokens > 0 {
		rem := max(e.caps.MaxTokens-spent, 0)
		_ = obj.Set("total", e.caps.MaxTokens)
		_ = obj.Set("remaining", rem)
	} else {
		_ = obj.Set("total", goja.Null())
		_ = obj.Set("remaining", goja.Null())
	}
	return obj
}

// emitDispatch announces a workflow agent as a top-level subagent-panel row. It
// reuses Name "task" so the existing TUI (isSubagentRootTool) renders it with no
// CLI change; the description arg becomes the row label.
func (e *Engine) emitDispatch(agentID, label, prompt string) {
	args, _ := json.Marshal(map[string]string{"description": label, "prompt": prompt})
	e.sink.Emit(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
		ID:   agentID,
		Name: "task",
		Args: string(args),
	}})
}

// emitResult closes a workflow agent's row with its terminal status.
func (e *Engine) emitResult(agentID, out string, err error) {
	t := event.Tool{ID: agentID, Name: "task", Output: out}
	if err != nil {
		t.Err = err.Error()
	}
	e.sink.Emit(event.Event{Kind: event.ToolResult, Tool: t})
}

// firstWords returns up to n space-separated words of s, single-spaced, for a
// compact panel label.
func firstWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
}
