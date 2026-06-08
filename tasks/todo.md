# Dynamic Workflows — Implementation TODO

Plan: [`docs/WORKFLOWS-PLAN.md`](../docs/WORKFLOWS-PLAN.md). Build top-to-bottom;
do not start Phase 1 until Phase 0 is green.

> Reuses the subagent `ParentID`-nesting panel shipped by the prior "Qwen-style
> subagent visibility" task (now complete) — so v1 needs **no** TUI change.

## Phase 0 — De-risk goja (BLOCKER) — DONE ✅
- [x] `go get github.com/dop251/goja` + `github.com/dop251/goja_nodejs/eventloop`
- [x] Confirm `CGO_ENABLED=0 go build ./...` passes + linux/arm64 cross-compile (single-binary intact)
- [x] `internal/workflow/host_test.go::TestGojaAsyncBridge` — ordered results `1!,2!,3!`, peak in-flight ≥ 2 (genuine concurrency). PASS
- [x] Pinned: goja `v0.0.0-20260607120635-348e6bea910d`, goja_nodejs `v0.0.0-20260212111938-1f56ff5bcf14` (recorded in plan §5)

## Phase 1 — Core engine — DONE ✅
- [x] `internal/workflow/engine.go` — `Engine` (goja loop, `sema chan struct{}`, agent-count + token budget, `runID`), `NewEngine(env, sink, caps, args, runID)`, `Run(ctx, script)`
- [x] `internal/workflow/host.go` — `__agent`/`__log`/`__phase`/`__budget` natives; `runOneAgent` bridging to `agent.RunSubAgent`
- [x] `internal/workflow/prelude.js` (`//go:embed`) — `agent`/`parallel`/`pipeline`/`log`/`phase`/`budget`; user script wrapped in top-level async IIFE → __done/__fail
- [x] `internal/workflow/sink.go` — `nestSink(agentID)` (ParentID tagging + token accrual)
- [x] Wrap parent sink in `event.Sync` (confirmed at `internal/event/sync.go:16`)
- [x] Exclude `run_workflow` + `SubagentMetaTools()` from workflow-agent registry
- [x] `internal/workflow/tool.go` — `WorkflowTool` (`run_workflow`), `ReadOnly()=false`, env mirrors `TaskTool`
- [x] Wired `NewWorkflowTool` at both `NewTaskTool` sites (`internal/boot/boot.go`, `internal/cli/acp.go`)
- [x] `[workflow]` config block: `max_concurrent/max_agents/max_tokens` (+ defaults). Gating via existing `[tools] enabled`, not a separate flag.
- [x] Each `agent()` emits synthetic ToolDispatch/ToolResult (Name="task") → renders in existing subagent panel, no CLI change
- [x] Caps enforced: agent-count rollback-on-reject, token budget pre-check, semaphore bounds concurrency (all unit-tested)
- [ ] Demo workflow: parallel code-review fan-out — pending a live run with a real provider (needs API key)

## Phase 1 — SPEC correction — DONE ✅
- [x] SPEC §1.3: dropped the false "TOML is the one accepted dependency" clause; kept pure-Go/CGO-free/single-binary rule, named the real deps
- [x] SPEC §2: added `workflow/` + a note listing the other existing packages
- [x] SPEC §3.10: new section describing the dynamic-workflow engine (no-DAG, goja bridge, caps, panel reuse)
- [x] `roach-code.example.toml`: documented `[workflow]`

## Phase 1 — Verification — DONE ✅
- [x] `CGO_ENABLED=0 go build ./...` = 0; linux/arm64 cross-compile = 0
- [x] `go vet` clean on touched packages; `gofmt -l` clean
- [x] 9 unit tests green: parallel ordering, await-dependency, args injection, object→JSON, max_agents rollback, max_tokens, syntax error, log/phase, goja async bridge
- [x] Full suite green EXCEPT two pre-existing seatbelt/sandbox tests (env-specific; proven failing on clean HEAD via `git stash -u`)
- [ ] Manual live run in `roach-code chat` (needs API key) — confirm panel + final-only context + token offloading vs inline loop

## ultragoal trigger — DONE ✅
- [x] `internal/cli/chat_ultragoal.go` — `ultragoalTask` parser, steering preamble, `startUltragoal`, shimmering banner
- [x] Detect `ultragoal`/`/ultragoal` prefix in chat submit handler (chat_tui.go enter case)
- [x] `ultragoalActive` field + `renderUltragoalBanner` pinned in View(); shimmers via shimmerPhase during the run
- [x] Clear banner on TurnDone (both paths in chat_turn.go)
- [x] Unit test `TestUltragoalTask` (10 cases) green; full cli suite green
- [x] Live headless proof: steering preamble reliably triggers run_workflow with parallel fan-out
- [x] Recon done via multi-agent workflow (4 Explore agents, cross-checked inline)

## Phase 2 — Ergonomics (after Phase 1)
- [ ] `schema` option → forced structured output
- [ ] per-agent `model` via `Config.ResolveModel`
- [ ] phase grouping / richer labels
- [ ] (optional) dedicated `Workflow*` event kinds + panel IFF piggyback UX insufficient

## Phase 3 — Scale & durability (later)
- [ ] worktree isolation for parallel writer agents
- [ ] background execution + cross-turn resume (completed-`agent()` journal)
- [ ] `web_search` builtin → research-class workflows
- [ ] `/workflows` picker + drill-in

## Review (Phase 1)

**Shipped.** A working `run_workflow` tool backed by `internal/workflow/` (engine,
host, prelude.js, sink, tool). The model writes a JS workflow; goja runs it
synchronously; each `agent()` fans out via the existing `agent.RunSubAgent`
behind a semaphore + agent-count + token caps. Intermediates stay in JS; only
the script's return value reaches the model.

**Deviations from the original research doc (all deliberate, see WORKFLOWS-PLAN.md):**
- No DAG structure / scheduler — control flow IS the graph (goja), reversing the
  doc's structured-TOML-spec recommendation (the goal is *genuine* dynamic flow).
- No new event kinds, no new TUI — each `agent()` piggybacks the existing
  subagent panel via synthetic ToolDispatch/ToolResult (Tool.ParentID). Saved
  ~3 files of TUI work; promote to a dedicated panel only if UX demands.
- Synchronous tool, not a daemon — no roster.json / cross-session resume in v1.
- No `[workflow] enabled` flag — the existing `[tools] enabled` filter already
  gates tools; avoided a zero-value-false footgun.
- Caps are config (8/200/0), not Claude Code's 16/1000 constants.

**Surprises / lessons:**
- goja **raw-string footgun**: backticks inside a Go raw-string (`` ` ``) Schema
  silently terminate the string → confusing "unexpected return" parse error.
  Used single quotes in JSON descriptions instead.
- The token budget came free: summing sub-agent `Usage.CompletionTokens` in
  `nestSink` needed zero extra metering — the Usage events already flow there.
- Two seatbelt sandbox tests fail in this nested-sandbox env; proven pre-existing
  via `git stash -u` → same failure on clean HEAD. Not caused by this work.

**Not done (needs a live API key):** end-to-end run in `roach-code chat` to eyeball
the panel and confirm context-offloading against an inline subagent loop.
