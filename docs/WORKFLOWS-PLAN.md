# Dynamic Workflows — Architecture & Implementation Plan

> **Status**: Implementation-ready plan. Supersedes the architectural proposal in
> [`WORKFLOWS-RESEARCH.md`](./WORKFLOWS-RESEARCH.md) §4–§6. This document is the
> one we build from. The research doc remains the external-findings record.
>
> **Goal (revised with maintainer)**: ship a *genuine* dynamic workflow engine —
> the model writes an imperative script (loops, conditionals, fan-out,
> accumulate-until-budget) that a background runtime executes, with intermediates
> living in **script variables, not the model's context window**. It does **not**
> need to be `/deep-research`; any useful dynamic workflow (e.g. parallel
> code-review fan-out) is a valid first target. This removes the `web_search`
> blocker entirely.

---

## 1. Why the research doc cannot be applied as-is (critique record)

Three subagents cross-checked every file reference in `WORKFLOWS-RESEARCH.md`
against the codebase. The doc is **~85% factually accurate** — most file
references are real — but has errors and one decision that the revised goal
reverses.

### 1.1 Confirmed (the doc did read the code)

| Claim | Verdict | Evidence |
|---|---|---|
| `internal/control/controller.go` transport-agnostic driver, 3 frontends | CONFIRMED | `controller.go:1-11`; `internal/serve/`, `internal/acp/`, `internal/cli/` |
| `internal/event/event.go:69` "Appended last…" comment | CONFIRMED | exact string present |
| `internal/agent/coordinator.go` planner+executor | CONFIRMED | matches SPEC §3.5 |
| `internal/agent/task.go` `RunSubAgent(...)` fan-out primitive | CONFIRMED | `task.go:220` |
| `internal/skill/` skills with `RunAs: RunSubagent` | CONFIRMED | `builtins.go` |
| `internal/tool/builtin/webfetch.go` SSRF-guarded | CONFIRMED | `webfetch.go:46-77` |
| `internal/jobs/jobs.go` session-scoped Manager via context | CONFIRMED | `NewManager`/`WithManager`/`FromContext` |

### 1.2 Factual errors to drop

| Doc claim | Reality |
|---|---|
| skill named `security_review` | skill is **`security-review`** (hyphen); only the *tool wrapper* is `security_review` (`skill/tools.go`) |
| "telemetry flows through `event.Usage`" | `event.Usage` is not a type; `Usage` is a field on `Event`, type `provider.Usage` |
| pricing in `internal/billing/` | pricing is `provider.Pricing` (`provider.go:226`); `billing/` only queries wallet balance |
| "mirror jobs.go's `sema chan struct{}`" | `jobs.Manager` has **no** semaphore; it spawns `go func()` **unbounded** (`jobs.go:125`). The cap is exactly what jobs *lacks* — it is net-new work, not a mirror |

### 1.3 The decision that flips: structured spec → embedded JS (goja)

The doc's §6 Q1 recommends a structured TOML `WorkflowSpec` over an embedded JS
runtime, on two grounds — **both false**:

1. *"goja would break the single-binary distribution."* **Wrong.** `goja` is
   pure Go, CGO-free; it builds fine under `CGO_ENABLED=0` and cross-compiles.
2. *"SPEC's lean-dependencies rule forbids it."* **Already moot.** SPEC §1.3 says
   "TOML parsing is the one accepted dependency," but `go.mod` already carries 13
   third-party deps (Bubble Tea, lipgloss, goldmark, chroma, …). The rule is
   aspirational, not enforced. **SPEC §1.3 must be corrected first** (see §6).

More decisively: a static TOML DAG **cannot express the defining feature** of a
dynamic workflow — `while (bugs.length < 10)`, dedup-against-seen, loop-until-dry,
`budget.remaining()` scaling. The doc itself concedes "genuinely imperative
control flow does not [fit]" (§6 Q1). A static DAG would ship something that
shares the *name* "workflow" but not the *essence*. Since the maintainer's goal
is a *genuine* dynamic workflow, **goja is the principled choice.**

### 1.4 Limits are Claude-Code economics, not roach-code's

16-concurrent / 1000-total come from Claude-on-Claude (~15× tokens). roach-code
drives cheap OpenAI-compatible endpoints (DeepSeek/MiMo). The caps are **config,
re-derived** (§4), not copied constants.

### 1.5 What the doc over-built (deferred)

- **5 new `Workflow*` event kinds + dedicated TUI panel + `/workflows` picker.**
  v1 piggybacks on existing `ToolDispatch`/`ToolResult` + the existing subagent
  panel (§3.4) — zero new kinds, zero TUI changes. Promote to Phase 2 only if UX
  demands it.
- **Daemon, `roster.json`, cross-session resume.** v1 is a synchronous tool;
  no daemon. Resume is Phase 3.
- **`web_search` / `cross_check`.** Not needed for the first workflow; Phase 3.

---

## 2. Design in one paragraph

A new `run_workflow` tool takes a JS script the model writes. The tool runs the
script **synchronously within the agent turn** on an embedded `goja` runtime
driven by a single-goroutine event loop. The script calls `agent(prompt, opts)`
(returns a `Promise`), `parallel([...])`, `pipeline(items, ...stages)`,
`log()`, `phase()`. Each `agent()` call routes through the **existing**
`agent.RunSubAgent` behind a per-run semaphore (concurrency cap) and budget
(agent-count + token caps). Each agent surfaces as a row in the **existing**
subagent panel by emitting synthetic `ToolDispatch`/`ToolResult`. The script's
return value becomes the tool result fed back to the model — intermediates never
touch its context. No daemon, no new event kinds, no new TUI in v1.

```
model turn
  └─ run_workflow(script)                [internal/tool → internal/workflow.Engine]
        ├─ goja loop (1 goroutine, owns Runtime)
        │     script: const r = await parallel(files.map(f => () => agent(`review ${f}`)))
        │
        ├─ __agent(opts)  ── returns Promise ──┐
        │     acquire sema slot                 │  (worker goroutine)
        │     emit synthetic ToolDispatch ──────┼──► session sink ──► subagent panel row
        │     agent.RunSubAgent(...) ───────────┘     (nested tool activity via ParentID)
        │     loop.RunOnLoop(resolve(result))         emit ToolResult ──► row summarized
        │
        └─ returns script's return value ──► tool result ──► model (final only)
```

---

## 3. Architecture (file-by-file, with real signatures)

### 3.1 New package `internal/workflow/`

| File | Responsibility |
|---|---|
| `engine.go` | `Engine`: owns the goja loop, semaphore, caps, budget. `Run(ctx, script) (string, error)`. One Engine per `run_workflow` call. |
| `host.go` | Native funcs bound into the runtime: `__agent`, `__log`, `__phase`, `__budget`. The Go↔JS bridge. |
| `prelude.js` (`//go:embed`) | JS layer: `agent`, `parallel`, `pipeline`, `log`, `phase`, `meta` handling, `budget` — built on the natives. |
| `sink.go` | `nestSink(agentID, parent)` — tags a sub-agent's events with `ParentID=agentID` (self-contained copy of `agent.subSinkFor`'s logic; see §3.4). |
| `tool.go` | `WorkflowTool` implementing `tool.Tool` — `run_workflow`. Constructed like `NewTaskTool` (needs the agent env), **not** a self-registering builtin. |
| `engine_test.go`, `host_test.go` | Unit tests incl. the goja async spike as a permanent regression test. |

### 3.2 Dependencies to add (pure Go, CGO-free)

```
github.com/dop251/goja                     # ES2017+ interpreter (async/await, Promise)
github.com/dop251/goja_nodejs/eventloop    # battle-tested microtask/macrotask loop
```

Both satisfy the *real* constraints (single binary, `CGO_ENABLED=0`,
cross-platform). The `eventloop` package correctly drains goja's promise-job
queue when promises resolve from Go goroutines — the one genuinely fiddly part —
so we adopt it rather than hand-roll. (We can vendor-minimize later if desired.)

### 3.3 The Go↔JS bridge (`host.go`) — the load-bearing 40 lines

`agent()` must return a `Promise` that resolves with a value produced on a
**worker goroutine**, while goja's Runtime is owned by the **loop goroutine**.
Pattern (goja-correct):

```go
// __agent(optsJSON string) returns a Promise. Bound into the runtime.
func (e *Engine) jsAgent(call goja.FunctionCall) goja.Value {
    optsJSON := call.Argument(0).String()
    p, resolve, reject := e.vm.NewPromise()       // created on the loop goroutine

    go func() {                                     // worker goroutine
        result, err := e.runOneAgent(e.ctx, optsJSON)   // calls agent.RunSubAgent
        e.loop.RunOnLoop(func(vm *goja.Runtime) {  // hop back onto the loop
            if err != nil { reject(vm.ToValue(err.Error())); return }
            resolve(vm.ToValue(result))
        })
    }()

    return e.vm.ToValue(p)
}
```

`runOneAgent`:
1. parse `{prompt, label, tools, model}` from `optsJSON`.
2. `e.budget.acquire()` — checks agent-count cap (return error → reject) and
   token budget; increments spawned count.
3. `e.sema <- struct{}{}` — blocks until a concurrency slot is free; `defer` release.
4. `agentID := fmt.Sprintf("wf-%s-%d", e.runID, n)`.
5. emit synthetic `ToolDispatch{Tool:{ID:agentID, Name:"task", Args:{description,prompt}}}` to `e.sink`.
6. `reg := agent.FilterRegistry(e.parentReg, tools, append(agent.SubagentMetaTools(), "run_workflow")...)`.
7. `out, err := agent.RunSubAgent(ctx, e.prov, reg, e.sysPrompt, prompt, agent.Options{MaxSteps:e.maxSteps, Temperature:e.temperature, Pricing:e.pricing, Gate:e.gate, ContextWindow:e.contextWindow, ArchiveDir:e.archiveDir}, nestSink(agentID, e.sink))`.
8. emit synthetic `ToolResult{Tool:{ID:agentID, Name:"task", Output:out, Err:…}}`.
9. return `(out, err)`.

> **Concurrency-safety**: steps 5/7/8 emit from worker goroutines, so `e.sink`
> **must** be `event.Sync`-wrapped (jobs.go uses the same rule: *"pass the
> session's synchronized sink (event.Sync) since jobs emit from goroutines"*).
> Wrap once in the Engine constructor: `e.sink = event.Sync(parentSink)`.

### 3.4 Reusing the subagent panel (zero TUI change)

`chat_subagent.go` already renders a panel row per `ToolDispatch` whose `Name` is
in `{task, explore, research, review, security_review}` (`isSubagentRootTool`),
and nests child tool activity by `ParentID`. By emitting each `agent()` as
`Name:"task"` with a unique top-level ID (`ParentID:""`) and passing
`nestSink(agentID, sink)` to `RunSubAgent`, every workflow agent gets its own
live row + nested tool cards **with no CLI code change**. `parallel()` → multiple
rows at once. The panel's `subagentLabel` reads the `description` arg, so we set
`Args:{"description": label}`.

`sink.go`:
```go
func nestSink(agentID string, parent event.Sink) event.Sink {
    return event.FuncSink(func(ev event.Event) {
        switch ev.Kind {
        case event.ToolDispatch, event.ToolResult:
            ev.Tool.ParentID = agentID
            ev.Tool.ID = agentID + "/" + ev.Tool.ID
            parent.Emit(ev)
        case event.Usage:
            ev.ParentID = agentID
            parent.Emit(ev)
        }
    })
}
```

### 3.5 The JS surface (`prelude.js`) the model writes against

Natives exposed from Go: `__agent(optsJSON) -> Promise`, `__log(msg)`,
`__phase(title)`, `__budget() -> {total, spent, remaining}`. The prelude wraps
them ergonomically:

```js
// agent(prompt, opts?) -> Promise<string>
globalThis.agent = (prompt, opts = {}) =>
  __agent(JSON.stringify({ prompt, ...opts }));

globalThis.log   = (m) => __log(String(m));
globalThis.phase = (t) => __phase(String(t));

// parallel(thunks) — barrier; failed thunk -> null (filter(Boolean) before use)
globalThis.parallel = (thunks) =>
  Promise.all(thunks.map(t => Promise.resolve().then(t).catch(() => null)));

// pipeline(items, ...stages) — each item flows through all stages independently
globalThis.pipeline = (items, ...stages) =>
  Promise.all(items.map(async (item, i) => {
    let v = item;
    for (const s of stages) { try { v = await s(v, item, i); } catch { return null; } }
    return v;
  }));

Object.defineProperty(globalThis, 'budget', { get: () => __budget() });
```

The model's script is appended after the prelude and must end with an expression
or `return`-equivalent (we wrap it: `(async () => { <script> })()` and resolve the
top-level promise to the tool result). Example the model would emit:

```js
const files = args.files;                       // args injected by the tool
const reviews = await pipeline(
  files,
  f => agent(`Review ${f} for bugs. Return findings as JSON.`, {label: `review ${f}`}),
  (r, f) => agent(`Verify these findings in ${f}, drop false positives:\n${r}`, {label: `verify ${f}`})
);
return JSON.stringify(reviews.filter(Boolean));  // only this reaches the model
```

### 3.6 `run_workflow` tool (`tool.go`)

```go
type WorkflowTool struct { /* same env fields as TaskTool: prov, pricing, parentReg, maxSteps, contextWindow, temperature, archiveDir, gate + cfg caps */ }

func (t *WorkflowTool) Name() string { return "run_workflow" }
func (t *WorkflowTool) ReadOnly() bool { return false }   // spawns writers; serial dispatch
func (t *WorkflowTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
    var p struct{ Script string `json:"script"`; Args json.RawMessage `json:"args"` }
    json.Unmarshal(args, &p)
    parentID, parentSink, _, _ := agent.CallContext(ctx)   // session sink + this call's id
    eng := workflow.NewEngine(t.env(), event.Sync(parentSink), p.Args)
    return eng.Run(ctx, p.Script)   // synchronous; returns script's final value
}
```

Wire it where `NewTaskTool` is wired (grep `NewTaskTool` — same assembly site),
guarded by `[workflow] enabled`.

### 3.7 Recursion / safety

- Workflow agents' registry **excludes** `run_workflow` + `SubagentMetaTools()`
  → no nested workflows, delegation stays one layer (mirrors `task`).
- Workflow agents are **non-interactive sub-agents** → the permission `Gate`
  resolves `Ask`→allow with no approver (SPEC §3.7), so **parallel agents never
  contend on an approval prompt**. This dissolves the "permission concurrency"
  worry for the prompt path. `deny` rules still bite.
- **Open risk**: parallel agents that *write the same files* can race. v1 ships
  with a doc warning + the existing sandbox confinement; **worktree isolation
  per writer agent is Phase 3** (the `EnterWorktree` analog).

---

## 4. Configuration

```toml
[workflow]
enabled        = true   # gates registration of run_workflow
max_concurrent = 8      # semaphore width (re-derived for cheap providers, not 16)
max_agents     = 200    # hard per-run agent-count cap (not 1000)
max_tokens     = 0      # 0 = no ceiling; else hard output-token budget per run
agent_model    = ""     # default model for agent(); "" = executor's model (v1: single model)
```

Caps enforced by the Engine, not the script (the script cannot raise them).

---

## 5. Risks & the de-risking spike (Phase 0, do first)

| Risk | Severity | Mitigation |
|---|---|---|
| goja `async`/`await` + `Promise` resolved from a Go goroutine | **HIGH** | **Phase 0 spike** (below). Blocks everything. |
| goja version lacks async/await | Medium | spike pins a known-good version |
| `event.Sync` missing / sink races | Medium | confirm `event.Sync` exists; wrap once |
| parallel agents writing same files | Medium | Phase 3 worktree isolation; v1 warns |
| per-agent multi-model | Low | v1 = single model; Phase 2 adds resolver |

**Phase 0 spike — DONE ✅** (`internal/workflow/host_test.go::TestGojaAsyncBridge`).
Verified on:
- `github.com/dop251/goja v0.0.0-20260607120635-348e6bea910d`
- `github.com/dop251/goja_nodejs v0.0.0-20260212111938-1f56ff5bcf14`
- Go 1.25.3, `event.Sync` confirmed at `internal/event/sync.go:16`.

Results: `await Promise.all([__agent(1),__agent(2),__agent(3)])` resolved from
worker goroutines returned ordered `"1!,2!,3!"` with peak in-flight ≥ 2 (genuine
concurrency). `CGO_ENABLED=0 go build ./...` **and** `GOOS=linux GOARCH=arm64`
cross-compile both pass — single-binary distribution intact. goja API used:
`vm.NewPromise()` → `(p, resolve, reject)`, `loop.RunOnLoop(...)` to hop back onto
the loop goroutine before touching the runtime, `p.State()`/`p.Result()` to drain.
**The async bridge is no longer a risk; Phase 1 is unblocked.**

The spike test (kept as a permanent regression):

```go
// proves: await agent() inside parallel(), resolved from worker goroutines,
// on goja + eventloop, returns ordered results.
func TestGojaAsyncBridge(t *testing.T) {
    // bind __agent that sleeps in a goroutine then resolves "<arg>!"
    // run: (async()=>{ return await Promise.all([1,2,3].map(n =>
    //        __agent(String(n)))) })()
    // assert result == ["1!","2!","3!"] and ran concurrently
}
```

If goja's async support proves inadequate, fallback is a generator-based driver
(also goja-supported) — but spike first.

---

## 6. Prerequisite: fix SPEC.md before coding

The whole goja rationale rests on SPEC §1.3 being already-violated. **Correct the
contract first** (SPEC says "code follows it"):

- SPEC §1.3: replace "TOML parsing is the one accepted dependency" with the real
  policy the codebase follows: *"third-party deps must be pure-Go, CGO-free, and
  not compromise the single-binary / cross-platform / distribution story"* (the
  rest of §1.3 already says this — just drop the false "one dependency" clause).
- SPEC §2 Layout: add the packages that already exist but aren't listed
  (`control`, `event`, `jobs`, `skill`, `serve`, `acp`, `sandbox`, `billing`, …)
  and the new `workflow`.
- SPEC §9 Roadmap: add "Dynamic workflows (see WORKFLOWS-PLAN.md)".

---

## 7. Phased plan

- **Phase 0 — Spike** (½ day): add goja deps; `TestGojaAsyncBridge` green.
- **Phase 1 — Core engine** (2–3 days): `internal/workflow/` (engine, host,
  prelude, sink); `run_workflow` tool wired behind `[workflow] enabled`; caps +
  budget; piggyback events (no TUI change). First demoable workflow: parallel
  code-review fan-out over changed files. SPEC.md corrected (§6).
- **Phase 2 — Ergonomics** (2 days): `schema` option (forced structured output
  via a one-shot tool); per-agent `model` via `Config.ResolveModel`; richer
  labels/phase grouping; *optionally* the dedicated `Workflow*` event kinds +
  panel **iff** piggyback UX is insufficient.
- **Phase 3 — Scale & durability** (later): worktree isolation for parallel
  writers; background execution + cross-turn resume (journal of completed
  `agent()` calls); `web_search` builtin → research-class workflows; `/workflows`
  picker + drill-in.

Acceptance for Phase 1: a model-authored script fans out N parallel review
agents, intermediates stay in JS, only the synthesized result enters the model's
context, caps are enforced, and live progress shows in the existing subagent
panel.
