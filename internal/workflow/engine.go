// Package workflow runs model-authored dynamic-workflow scripts on an embedded
// JavaScript runtime (goja). A workflow is a script the model writes; its
// control flow IS the dependency graph — `await` sequences dependent work,
// `Promise.all` / `parallel` fans it out — so there is deliberately no DAG data
// structure and no scheduler here. The engine owns only what the script must NOT
// control: a concurrency semaphore, agent-count and token budgets, and the
// bridge from each `agent()` call to the existing sub-agent machinery
// (agent.RunSubAgent). Intermediate results live in script variables, never the
// model's context window; only the script's return value is fed back as the
// run_workflow tool result.
package workflow

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"

	"roach-code/internal/agent"
	"roach-code/internal/event"
	"roach-code/internal/provider"
	"roach-code/internal/tool"
)

//go:embed prelude.js
var preludeJS string

// Caps bound a single workflow run. The script cannot raise them — they are
// enforced by the engine, not the JS.
type Caps struct {
	MaxConcurrent int   // simultaneous agents (semaphore width)
	MaxAgents     int   // total agent() calls allowed per run
	MaxTokens     int64 // output-token ceiling per run; 0 = no ceiling
}

func (c Caps) normalized() Caps {
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 8
	}
	if c.MaxAgents <= 0 {
		c.MaxAgents = 200
	}
	if c.MaxTokens < 0 {
		c.MaxTokens = 0
	}
	return c
}

// Env is the sub-agent execution environment a workflow's agents run in. It
// mirrors the fields agent.NewTaskTool captures, so workflow agents reuse the
// parent's provider, tool registry, model budget, and permission gate.
type Env struct {
	Prov          provider.Provider
	Pricing       *provider.Pricing
	ParentReg     *tool.Registry
	MaxSteps      int
	ContextWindow int
	Temperature   float64
	ArchiveDir    string
	SysPrompt     string // "" → agent.DefaultTaskSystemPrompt
	Gate          agent.Gate
}

// runResult is the engine's terminal outcome, delivered from the JS side via
// __done / __fail (or a synchronous script error).
type runResult struct {
	val string
	err error
}

// Engine runs one workflow script to completion. Construct one per run_workflow
// call via NewEngine; Run is single-shot and not reusable.
type Engine struct {
	env   Env
	caps  Caps
	sink  event.Sink // synchronized: workflow agents emit from worker goroutines
	args  json.RawMessage
	runID string

	ctx  context.Context
	loop *eventloop.EventLoop
	vm   *goja.Runtime

	sema    chan struct{}
	spawned atomic.Int64 // agent() calls started (against MaxAgents)
	tokens  atomic.Int64 // output tokens consumed so far (CompletionTokens summed)
	seq     atomic.Int64 // per-agent id counter
}

// NewEngine builds an engine for one run. sink is the session's event sink
// (the synthetic per-agent dispatch/result events render in the existing
// subagent panel); it is wrapped with event.Sync because workflow agents emit
// from worker goroutines. caps are normalized to safe defaults.
func NewEngine(env Env, sink event.Sink, caps Caps, args json.RawMessage, runID string) *Engine {
	if env.SysPrompt == "" {
		env.SysPrompt = agent.DefaultTaskSystemPrompt
	}
	if sink == nil {
		sink = event.Discard
	}
	caps = caps.normalized()
	return &Engine{
		env:   env,
		caps:  caps,
		sink:  event.Sync(sink),
		args:  args,
		runID: runID,
		sema:  make(chan struct{}, caps.MaxConcurrent),
	}
}

// Run executes script to completion and returns its final value (the resolved
// value of the top-level async wrapper, stringified). It blocks until the script
// settles or ctx is cancelled. The goja runtime is owned by a single event-loop
// goroutine; agent() work happens on worker goroutines and resolves back onto
// the loop (see host.go).
func (e *Engine) Run(ctx context.Context, script string) (string, error) {
	e.ctx = ctx
	e.loop = eventloop.NewEventLoop()
	e.loop.Start()
	defer e.loop.Stop()

	done := make(chan runResult, 1)

	e.loop.RunOnLoop(func(vm *goja.Runtime) {
		e.vm = vm
		e.install(vm, done)
		full := preludeJS + "\n" + wrapUserScript(script)
		if _, err := vm.RunString(full); err != nil {
			send(done, runResult{err: fmt.Errorf("workflow script: %w", err)})
		}
	})

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-done:
		return r.val, r.err
	}
}

// install binds the native functions and the args global into the runtime. Runs
// on the loop goroutine.
func (e *Engine) install(vm *goja.Runtime, done chan<- runResult) {
	if len(e.args) > 0 {
		var v any
		if json.Unmarshal(e.args, &v) == nil {
			_ = vm.Set("args", vm.ToValue(v))
		}
	}
	_ = vm.Set("__agent", e.jsAgent)
	_ = vm.Set("__log", e.jsLog)
	_ = vm.Set("__phase", e.jsPhase)
	_ = vm.Set("__budget", e.jsBudget)
	_ = vm.Set("__done", func(s string) { send(done, runResult{val: s}) })
	_ = vm.Set("__fail", func(s string) { send(done, runResult{err: errors.New(s)}) })
}

// send delivers the first settlement and drops any later ones (the script can
// only finish once; a redundant signal must never block the loop goroutine).
func send(ch chan<- runResult, r runResult) {
	select {
	case ch <- r:
	default:
	}
}

// wrapUserScript runs the model's script inside a top-level async IIFE and pipes
// its settlement to __done / __fail. A non-string return is JSON-stringified; an
// undefined/null return becomes "" so a workflow that only logs returns cleanly.
func wrapUserScript(script string) string {
	return ";(async () => {\n" + script + "\n})().then(" +
		"function (v) { __done(v === undefined || v === null ? '' : (typeof v === 'string' ? v : JSON.stringify(v))); }," +
		"function (e) { __fail(String((e && e.stack) || e)); });"
}
