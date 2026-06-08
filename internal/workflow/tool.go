package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"roach-code/internal/agent"
	"roach-code/internal/event"
)

// WorkflowTool is the `run_workflow` builtin: the model emits a JavaScript
// workflow script, the tool runs it synchronously on an Engine and returns the
// script's final value. It is constructed with the agent's environment (like
// agent.NewTaskTool), not self-registered, because it needs the parent
// provider, registry, and permission gate to spawn sub-agents.
type WorkflowTool struct {
	env  Env
	caps Caps
}

// NewWorkflowTool wires the tool to the parent agent's environment.
func NewWorkflowTool(env Env, caps Caps) *WorkflowTool {
	return &WorkflowTool{env: env, caps: caps}
}

func (t *WorkflowTool) Name() string { return "run_workflow" }

func (t *WorkflowTool) Description() string {
	return "Run a dynamic workflow: a JavaScript script you write that fans out work across many sub-agents whose intermediate results stay in script variables — not your context window — so only the script's final return value comes back to you. Use it when a task decomposes into many independent sub-tasks (e.g. review/analyse N files in parallel, multi-angle research, map-reduce over a list) and you want the synthesis, not every intermediate.\n\n" +
		"The script runs on an embedded runtime with these globals:\n" +
		"  agent(prompt, opts?) -> Promise<string>   spawn one sub-agent; await its final answer. opts: {label?, tools?: string[]}.\n" +
		"  parallel(thunks)     -> Promise<any[]>     run thunks (() => agent(...)) concurrently; a failed one becomes null.\n" +
		"  pipeline(items, ...stages) -> Promise<any[]>  each item flows through every stage; stage gets (prev, item, index).\n" +
		"  log(msg) / phase(title)                    progress lines shown to the user.\n" +
		"  args                                       the JSON value you pass as `args`.\n" +
		"  budget -> {total, spent, remaining, agents, maxAgents}  live caps snapshot.\n\n" +
		"Dependencies are expressed by ordinary control flow: `const a = await agent(...); const b = await agent(... a ...)` makes b depend on a. End the script with a `return` of the final result (a string, or any JSON-serialisable value). Sub-agents cannot themselves call run_workflow. Concurrency, agent-count, and token budgets are enforced by the runtime.\n\n" +
		"Example:\n" +
		"  const files = args.files;\n" +
		"  const reviews = await parallel(files.map(f => () => agent(`Review ${f} for bugs; return findings.`, {label: `review ${f}`})));\n" +
		"  return JSON.stringify(reviews.filter(Boolean));"
}

func (t *WorkflowTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "script":{"type":"string","description":"The JavaScript workflow script. Uses agent()/parallel()/pipeline()/log()/phase()/args/budget. End with 'return <final value>'."},
  "args":{"description":"Optional JSON value exposed to the script as the global 'args' (e.g. a list of files, a question)."}
},
"required":["script"]
}`)
}

// ReadOnly is false: the workflow spawns sub-agents that may call writer tools.
// This also keeps run_workflow off the parallel-dispatch path so two workflows
// can't run at once in a single turn.
func (t *WorkflowTool) ReadOnly() bool { return false }

func (t *WorkflowTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Script string          `json:"script"`
		Args   json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Script) == "" {
		return "", errors.New("script is required")
	}

	// The call context carries the session sink and this call's id; the engine
	// nests each workflow agent's events under a synthetic id derived from it so
	// they render in the subagent panel. A headless context (no sink) still runs.
	parentID, sink, _, ok := agent.CallContext(ctx)
	if !ok || sink == nil {
		sink = event.Discard
		parentID = "run"
	}

	eng := NewEngine(t.env, sink, t.caps, p.Args, parentID)
	return eng.Run(ctx, p.Script)
}
