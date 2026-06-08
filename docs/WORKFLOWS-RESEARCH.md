# Dynamic Workflows — Research Notes

> **Status**: Research notes for an unimplemented feature. **Not normative.**
> The normative contract is [`SPEC.md`](./SPEC.md). This document captures
> external findings + a proposed shape for `internal/workflow/`. It will be
> folded into `SPEC.md §9 Roadmap` once the maintainer signs off on the
> architectural choices in §6.

## 0. Korean TL;DR (사용자 요약)

Claude Code의 `/deep-research`와 **dynamic workflow**는 본질적으로 *Claude가 작성하는 JavaScript 스크립트*를 백그라운드 런타임이 실행하는 구조입니다. 중간 결과는 메인 세션의 context window가 아니라 **script variables**에 보관되어, run당 최대 **1,000 agents**, 동시 **16개**까지 fan-out할 수 있습니다. `/deep-research`는 Anthropic Research 시스템의 **orchestrator-worker** 패턴(`LeadResearcher` → 3-5 parallel subagents → `CitationAgent`)을 *adversarial voting*으로 감싼 것입니다.

roach-code에서는 **goja 임베딩 대신 구조화된 `WorkflowSpec`** (TOML/JSON) + **`workflow.Manager`** (기존 `internal/jobs` 패턴 미러) + **`event.Workflow*` kinds**로 구현하는 것이 SPEC의 "lean dependencies" 원칙과 transport-agnostic `control.Controller`와 가장 잘 맞습니다. TUI는 `/workflows` picker → `WorkflowRun` (phase별 진행) → `WorkflowAgent` (drill-in) 3단 drill-down으로, 기존 chat TUI의 stacked-region 패턴 위에 적층합니다.

자세한 검증 결과, 인용, 기각된 claim, 권장 구현 단계는 본문 §1–§7 참조.

---

## 1. What "Dynamic Workflow" Actually Is in Claude Code

A **dynamic workflow** is a JavaScript script that Claude writes, executed by a
runtime in the background while the main session stays responsive. Intermediate
results live in **script variables**, not in Claude's context window — this is
what makes the pattern scale beyond what a single subagent loop can do.

| Aspect | Subagents / Skills | **Dynamic Workflows** |
|---|---|---|
| Where intermediates live | Claude's context window | **Script variables** |
| Scale | 1–few | **up to 1,000 agents / run, 16 concurrent** |
| User input mid-run | allowed | **not allowed** |
| Filesystem / shell from the script | allowed | **denied** — only spawned agents can |
| Resume after pause | n/a | **within the same Claude Code session** |

> Source: [code.claude.com/docs/en/workflows.md](https://code.claude.com/docs/en/workflows.md),
> [code.claude.com/docs/en/agents.md](https://code.claude.com/docs/en/agents.md)

### 1.1 The bundled `/deep-research` workflow

Verbatim from the docs:

> *"Fans out web searches on a question across several angles, fetches and
> cross-checks the sources it finds, **votes on each claim**, and returns a
> cited report with **claims that didn't survive cross-checking filtered out**."*

The cross-checking + voting is a concrete instance of the *"independent agents
adversarially reviewing each other"* pattern the docs describe for workflows
generally.

### 1.2 Hard runtime limits

- **16 concurrent agents** (fewer on CPU-limited machines)
- **1,000 total agents per run**
- No mid-run user input
- No direct filesystem or shell access from the workflow script
- Runs are **resumable within the same Claude Code session**; stopping caches
  completed agent results, resuming re-runs only the remaining agents

> Source: [code.claude.com/docs/en/workflows.md](https://code.claude.com/docs/en/workflows.md) — "Behavior and limits" table, "Resume after a pause" section

---

## 2. Anthropic's Production Research Architecture

The engineering post ([built-multi-agent-research-system](https://www.anthropic.com/engineering/built-multi-agent-research-system),
[multi-agent-research-system](https://www.anthropic.com/engineering/multi-agent-research-system))
describes the production system `/deep-research` is built on. Direct quotes
(verified, 3-0 adversarial):

```
[User query]
       │
       ▼
┌─────────────────────────────────────────┐
│   LeadResearcher (orchestrator)         │
│   • analyses query                      │
│   • persists plan to external Memory    │
│   • survives the 200K-token truncation  │
└─────────────────────────────────────────┘
       │ spawns 3–5 specialised subagents in parallel
       ▼
┌──────────┬──────────┬──────────┬──────────┐
│Subagent  │Subagent  │Subagent  │Subagent  │   ← each with its own
└──────────┴──────────┴──────────┴──────────┘     objective/format/tools/sources
       │ stores work in external systems,
       │ returns only lightweight references
       ▼
[LeadResearcher synthesis]
       │ decides: more research? or converge?
       ▼ (on convergence)
┌─────────────────────────────────────────┐
│   CitationAgent                         │
│   • processes docs + report             │
│   • identifies specific citation points │
└─────────────────────────────────────────┘
       │
       ▼
[Final cited report to user]
```

**Key quotes** (verbatim):

- *"multi-agent architecture with an orchestrator-worker pattern… specialised
  subagents that operate in parallel"*
- *"spinning up 3-5 subagents in parallel rather than serially"*
- *"Subagents call tools to store their work in external systems, then pass
  lightweight references back to the coordinator… prevents information loss
  during multi-stage processing"*
- *"If the context window exceeds 200,000 tokens it will be truncated and it
  is important to retain the plan"*
- *"Once sufficient information is gathered, the system exits the research
  loop and passes all findings to a CitationAgent"*

### 2.1 Effort scaling rules

| Complexity | Scale | Tool calls |
|---|---|---|
| Simple fact-finding | 1 agent | 3–10 |
| Direct comparisons | 2–4 subagents | 10–15 each |
| Complex research | 10+ subagents | clearly divided responsibilities |

**3–5 parallel subagents + parallel tool calls** cut research time by *up to
90%* for complex queries (best case, benchmark undisclosed — medium
confidence). **Trade-off**: multi-agent uses **~15× more tokens** than
single-agent chat.

### 2.2 LangGraph supervisor pattern (canonical reference)

The closest published reference architecture:

- **Supervisor** = an LLM-driven agent whose *available tools are the other agents*
- Each worker has its **own private scratchpad**
- Workers' final responses are **appended to a global scratchpad**
- Contrast with the multi-agent-collaboration pattern (single shared scratchpad,
  real-time stream)

> Source: [langchain.com/blog/langgraph-multi-agent-workflows](https://www.langchain.com/blog/langgraph-multi-agent-workflows)
> — medium confidence (2024 vendor blog, pattern still canonical in 2026)

---

## 3. The Workflow TUI in Claude Code

`/workflows` lists running and completed runs; selecting one opens a progress
view that shows each phase with agent counts, token totals, and elapsed time.

### 3.1 Keymap (from the "Watch the run" table)

| Key | Action |
|---|---|
| ↑/↓ | select phase or agent |
| Enter / → | drill phase → agent (prompt, recent tool calls, result) |
| Esc | back out one level |
| j/k | scroll overflowing agent detail |
| p | pause/resume the run or selected agent |
| x | stop the selected agent / the whole run |
| r | restart a running agent |
| s | save the run's script as a command |

### 3.2 Background infrastructure

A **per-user supervisor process** hosts background sessions, separate from the
terminal and agent view, auto-started the first time a session goes to
background. State persists at:

- `~/.claude/daemon/roster.json` — running sessions
- `~/.claude/jobs/<id>/state.json` — per-session state

A running subagent, dynamic workflow, monitor, or background shell command
counts as "active work" and keeps the session alive across the ~1-hour idle
stop.

> Source: [code.claude.com/docs/en/agent-view.md](https://code.claude.com/docs/en/agent-view.md)

### 3.3 Agent view TUI (related)

A fullscreen terminal table grouped by state (Pinned, Ready for review, Needs
input, Working, Completed). State conveyed by icon colour/animation. Dispatch
input at the bottom accepts prefixes:

- `<agent-name>` or `@agent-name` → route to a subagent as the session's main agent
- `@<repo>` → run in another repo
- `!command` → run a shell background job
- `#<number>` / PR URL → select an existing session

Background sessions are **isolated by being moved into git worktrees** under
`.claude/worktrees/` before editing files.

> Source: [code.claude.com/docs/en/agent-view.md](https://code.claude.com/docs/en/agent-view.md)

---

## 4. Where the Workflow Machinery Should Live in roach-code

The natural fit given the existing architecture:

### 4.1 New package: `internal/workflow/`

Mirror `internal/jobs/` (session-scoped background Manager exposed via
context). Files:

| File | Role | Reference |
|---|---|---|
| `workflow.go` | `Workflow` (per-run), `Phase`, `Agent` types | shape of `internal/jobs/jobs.go` |
| `manager.go` | `Manager` (16-cap / 1,000-cap enforced), `WithManager`/`FromContext` | `NewManager`/`FromContext` pattern from `internal/jobs/jobs.go:90-95` |
| `spec.go` | `WorkflowSpec` parser (TOML/JSON) — Claude emits, runtime interprets | DAG of phases, each phase = list of `runAs=subagent` skill invocations |
| `runtime.go` | DAG topological execution, concurrency caps, resume | caps enforced by the **manager**, not the script |
| `resume.go` | on-disk resume cache (Phase 2; see §6 Q3) | `~/.roach-code/workflows/<id>/state.json` |

Sketch:

```go
// internal/workflow/workflow.go
package workflow

type Workflow struct {
    ID     string
    Spec   WorkflowSpec
    Phases []*Phase
    Status Status
}

type Phase struct {
    Name      string
    Skill     string  // "explore", "research", "review", "security_review", …
    Parallel  int
    DependsOn []string
    Agents    []*Agent
}

type Agent struct {
    ID                string
    Prompt            string
    Status            Status
    Tokens            int64
    Result            string
    StartedAt, FinishedAt time.Time
}

type Spec struct {
    Name   string
    Phases []PhaseSpec
    Budget *Budget  // max tokens, max agents
}
```

```go
// internal/workflow/manager.go — concurrency cap enforced here, not the script
type Manager struct {
    sink     event.Sink
    root     context.Context
    cancel   context.CancelFunc

    mu      sync.Mutex
    runs    map[string]*Workflow
    sema    chan struct{}  // 16-cap
    cap     int            // 1000-total
    spawned int
}

func NewManager(sink event.Sink) *Manager {
    return &Manager{
        sink:  sink,
        runs:  map[string]*Workflow{},
        sema:  make(chan struct{}, 16),
        cap:   1000,
    }
}

func (m *Manager) acquireSlot(ctx context.Context) error {
    select {
    case m.sema <- struct{}{}:
        m.spawned++
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

### 4.2 Event kinds — append to `internal/event/event.go`

The event package's policy (line 69 of the existing file) is *"Appended last
to keep the Kind values before it wire-stable."* Follow that pattern:

```go
// internal/event/event.go — append at the end of the const block
const (
    // ... existing Kinds unchanged ...

    // WorkflowStarted marks a new workflow run (Workflow: ID/Spec/PhaseCount).
    // Emitted when the runtime accepts a WorkflowSpec and spawns the first phase.
    WorkflowStarted
    // PhaseStarted marks one phase starting (Workflow: WorkflowID/PhaseID/Name).
    PhaseStarted
    // AgentStarted marks one sub-agent starting inside a phase (Workflow:
    // WorkflowID/PhaseID/AgentID/Prompt). Mirrors ToolDispatch.
    AgentStarted
    // AgentFinished marks one sub-agent finishing (Workflow: WorkflowID/PhaseID/
    // AgentID/Status/Tokens/Result). Mirrors ToolResult.
    AgentFinished
    // WorkflowDone marks terminal state of a workflow (Workflow: ID/Status/
    // AggregatedResult). Always the last event of a workflow run. Mirrors TurnDone.
    WorkflowDone
)
```

Because `control.Controller` is the single transport-agnostic session driver
behind every frontend (chat TUI, desktop, HTTP/SSE), appending these kinds
gives all frontends the workflow view for free.

### 4.3 Tooling integration

Add new builtins in `internal/tool/builtin/`:

1. `run_workflow` — Claude emits a `WorkflowSpec` (TOML), Manager dispatches
2. `cross_check` — given a list of claims, spawns N agents to *refute* them
   (the "adversarial voting" primitive from `/deep-research`)
3. `web_search` — **currently missing from roach-code**; see §6 Q2

Phase agents still go through `internal/agent/task.go`'s existing sub-agent
machinery and still go through the existing permission gate in
`internal/permission/`. The workflow runtime adds *orchestration* above that
existing layer, not a parallel one.

### 4.4 What does NOT need to change

- `internal/control/controller.go` — only subscribers added
- `internal/sandbox/`, `internal/permission/` — spawned agents are still
  regular sub-agents
- `internal/billing/` — token telemetry already flows through `event.Usage`

---

## 5. How the TUI Should Be Drawn

roach-code's chat TUI is a Bubble Tea model with **stacked regions** (top
to bottom): transcript, todo panel, subagent panel, input. All driven by the
same `event.Sink`. The workflow view is a new stacked mode, following the
same pattern as `chat_todo.go` and `chat_subagent.go`.

### 5.1 `/workflows` picker — top-level entry

Mirrors `internal/cli/jobs_picker.go`:

```
╭─ Workflows ─────────────────────────────────────────────╮
│ ● deep-research  claude-code/roac…   5 phases  12/16  ◀  │
│ ◐ review-batch   roach-code          3 phases   4/16    │
│ ✓ migrate-...    old-project         done              │
│ ✗ failed-build   somewhere           failed            │
│                                                         │
│ ↑/↓ select · Enter drill in · Esc close                  │
╰─────────────────────────────────────────────────────────╯
```

Icons follow the Claude Code agent view convention: `●` running, `◐` paused,
`✓` completed, `✗` failed.

### 5.2 `WorkflowRun` view — phase progress

Stacked above the input box (same place as the existing subagent panel):

```
╭─ Workflow: deep-research ─────────────────────── ⏱ 4m 12s ─╮
│ Phase 1 · plan              ✓ done     1 agent   2.3k tok   │
│ Phase 2 · fan-out-search    ● running  4/5       8.1k tok   │
│   ├─ search "roach-code ar…  ● active   1.2k tok             │
│   ├─ search "internal/jobs" ✓ done     0.8k tok             │
│   ├─ search "control/contr" ● active   1.5k tok             │
│   └─ search "event/event.g" ◐ queued                       │
│ Phase 3 · cross-check       ◐ waiting  —                    │
│ Phase 4 · synthesize        ◐ waiting  —                    │
│                                                            │
│ p pause · x stop · r restart selected · s save · Esc back  │
╰────────────────────────────────────────────────────────────╯
```

Implementation reuses the row-pattern from `chat_todo.go`:
`WorkflowStarted`/`PhaseStarted`/`AgentStarted`/`AgentFinished`/`WorkflowDone`
events → `tea.Msg` → `Update()` redraws a row list.

### 5.3 `WorkflowAgent` view — drill-in

```
╭─ Agent: search "roach-code architecture" ───────── ✕ stop ─╮
│ Status:  running · 1.2k tok · 3 tool calls · 8s           │
│                                                           │
│ Prompt:                                                   │
│   Search the web for "roach-code architecture" and        │
│   summarize 3 official sources.                           │
│                                                           │
│ Tool calls:                                               │
│   ✓ web_search    "roach-code architecture"   0.4s        │
│   ✓ web_fetch     https://github.com/…/ROACH-CODE.md 1.2s │
│   ● web_fetch     https://github.com/…/SPEC.md     0.6s   │
│                                                           │
│ j/k scroll · Esc back · r restart · x stop                │
╰───────────────────────────────────────────────────────────╯
```

### 5.4 Unified keymap (picker / run / agent)

| Key | picker | run view | agent view |
|---|---|---|---|
| ↑/↓ | select run | select phase | select tool call |
| Enter / → | drill-in | drill-in | (drill-in leaf) |
| Esc | close | back to picker | back to run view |
| j/k | — | — | overflow scroll |
| p | — | run pause/resume | agent pause/resume |
| x | — | run stop | agent stop |
| r | — | — | agent restart |
| s | — | spec save | — |

`s` writes the spec to `.roach-code/workflows/<id>.md` (a reusable command
the model loads via `run_skill` next time).

---

## 6. Open Questions / Decisions for the Maintainer

These came out of the adversarial verification as *unresolved by external
sources* — each needs a sign-off before implementation begins.

### Q1. JS runtime vs structured spec

Should roach-code embed a JS runtime (goja / deno) to match Claude Code's
*"Claude writes JavaScript"* model, or stay with a structured `WorkflowSpec`
interpreted by a Go runtime?

- **Recommendation**: structured spec. SPEC's "lean dependencies" rule
  ([SPEC.md §1.3](SPEC.md)) and the existing sub-agent machinery both point
  this way. goja would break the single-binary distribution.
- **Trade-off**: a spec is less expressive than arbitrary JS. Most workflow
  patterns (DAG, parallel, dependency, loop-with-cap) fit a spec; genuinely
  imperative control flow does not.

### Q2. Vendor a `web_search` tool?

`/deep-research` quality (fan-out + cross-checking + adversarial voting)
cannot be implemented honestly without a search primitive. `web_fetch`
exists (SSRF-guarded) but expects a URL — the *fan-out starting point* is a
query, not a URL.

- **Option A**: third-party search vendor (Tavily, Brave, Serper, …)
- **Option B**: configured external sources (URLs, local docs index) — the
  cross-checker still operates, but the fan-out is narrower

### Q3. Disk-persist vs session-scoped

Anthropic persists at `~/.claude/daemon/roster.json` + `~/.claude/jobs/<id>/state.json`.
roach-code's `internal/jobs` Manager is session-scoped.

- **Recommendation**: Phase 1 = session-scoped (matches Anthropic's
  *"resumable within the same Claude Code session, fresh on next launch"*);
  Phase 2 = disk-persist after the feature stabilises.
- The `~/.claude/tasks/{team-name}/` precedent argues for disk-persist if
  cross-session resume is wanted.

### Q4. Per-workflow token budget

Multi-agent is ~15× more expensive than single-agent chat. roach-code's
`internal/billing/` has per-provider pricing but no per-workflow cap or
warning. A `/deep-research` equivalent without a hard ceiling could surprise
users. The `Spec.Budget` field (§4.1) should be enforced by the Manager.

---

## 7. Caveats and Verification Notes

### 7.1 What is uncertain / scope-limited

- Claude Code's dynamic-workflow feature is in **research preview** as of
  v2.1.154–161 (2026-06). Docs explicitly say the runtime may evolve; the
  cited keymap and limits could shift before the feature stabilises.
- The **90% time-reduction** figure has no disclosed methodology. Treat as a
  design signal, not a measured promise.
- The **LeadResearcher / CitationAgent** naming is from Anthropic's
  *production Research product*, not the Claude Code `/deep-research`
  *command* itself. The docs page describes the command's behaviour but does
  not name internal agents. The pattern is consistent, but the bundled
  command's internal agent names are not publicly documented.
- roach-code has `web_fetch` (builtin) but **no `web_search`** and **no
  cross-checking / voting primitives** today. Implementing `/deep-research`
  quality requires both.
- LangGraph supervisor pattern (§2.2) is from a 2024 vendor blog; pattern
  is still canonical in 2026 but surface API has evolved.

### 7.2 Claims that were refuted by adversarial verification

| Claim | Vote | Why killed |
|---|---|---|
| Effort scaling is encoded directly in prompts as explicit rules | 1–2 | The 1–4–10+ scaling is an *Anthropic Research product* design decision, not the *bundled workflow prompt*. Direct quote not findable in primary sources. |
| CitationAgent is a compressor for subagents + has a separate synthesis step | 0–3 | CitationAgent's *input* is documents/report, not subagents. The "compressor" framing is over-generalisation. |
| LangGraph models multi-agent as a graph with shared state object (not scratchpad) | 0–3 | Primary source explicitly says *global scratchpad*; the claim contradicts it. |
| Autonomous agent = Planning + Memory (vector store, MIPS) + Tool Use | 1–2 | Lilian Weng's 2023 post is a *single-agent* framework, not multi-agent workflow architecture — scope mismatch. |

### 7.3 Verification statistics

- 5 search angles × 5 search agents = 25 search agents
- 26 sources fetched, URL-deduped to 26 unique
- 123 claims extracted, top 25 verified
- 21 confirmed, 4 killed
- 12 final synthesised claims (semantic duplicates merged)
- 108 total agent calls across the workflow

---

## 8. Source Index

**Primary (Claude Code docs)**

- [code.claude.com/docs/en/workflows.md](https://code.claude.com/docs/en/workflows.md)
- [code.claude.com/docs/en/agent-view.md](https://code.claude.com/docs/en/agent-view.md)
- [code.claude.com/docs/en/agents.md](https://code.claude.com/docs/en/agents.md)
- [code.claude.com/docs/en/interactive-mode.md](https://code.claude.com/docs/en/interactive-mode.md)

**Primary (Anthropic engineering)**

- [anthropic.com/engineering/built-multi-agent-research-system](https://www.anthropic.com/engineering/built-multi-agent-research-system)
- [anthropic.com/engineering/multi-agent-research-system](https://www.anthropic.com/engineering/multi-agent-research-system)

**Reference architecture**

- [langchain.com/blog/langgraph-multi-agent-workflows](https://www.langchain.com/blog/langgraph-multi-agent-workflows)
- [lilianweng.github.io/posts/2023-06-23-agent/](https://lilianweng.github.io/posts/2023-06-23-agent/)

**roach-code codebase** (cited in §4)

- `ROACH-CODE.md` — project conventions (one Controller, cache-first, Go kernel under `internal/`)
- `docs/SPEC.md` — §1.3 lean dependencies; §3.4 Agent, §3.5 Coordinator; §9 Roadmap (currently no workflows)
- `internal/control/controller.go` — transport-agnostic session driver
- `internal/jobs/jobs.go` — template for session-scoped background Manager exposed via context
- `internal/agent/task.go` — existing sub-agent machinery (the right primitive to fan out from)
- `internal/agent/coordinator.go` — planner+executor two-model pattern
- `internal/skill/skill.go`, `internal/skill/builtins.go` — `runAs=subagent` skills (explore/research/review/security_review)
- `internal/event/event.go` — append new `Workflow*` event Kinds here
- `internal/cli/chat_todo.go`, `internal/cli/chat_subagent.go` — templates for stacked-region TUI panels
- `internal/cli/jobs_picker.go`, `internal/cli/resume_picker.go` — templates for the `/workflows` picker
- `internal/tool/builtin/webfetch.go` — SSRF-guarded fetch; missing counterpart `web_search`
