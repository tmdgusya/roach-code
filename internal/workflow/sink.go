package workflow

import "roach-code/internal/event"

// nestSink returns the event.Sink a workflow agent's sub-agent emits to. It
// forwards only the activity worth showing — tool dispatch/result and usage —
// tagging each with the agent's id so the existing subagent panel nests the
// agent's tool cards under its row (the same convention agent.subSink uses for
// `task`). The agent's own text/reasoning/turn events are dropped: only the
// agent's final answer (returned by RunSubAgent) matters to the workflow.
//
// It also accumulates the agent's output tokens into the run's token budget —
// no separate metering path needed.
func (e *Engine) nestSink(agentID string) event.Sink {
	parent := e.sink
	return event.FuncSink(func(ev event.Event) {
		switch ev.Kind {
		case event.ToolDispatch, event.ToolResult:
			ev.Tool.ParentID = agentID
			ev.Tool.ID = agentID + "/" + ev.Tool.ID
			parent.Emit(ev)
		case event.Usage:
			ev.ParentID = agentID
			parent.Emit(ev)
			if ev.Usage != nil {
				e.tokens.Add(int64(ev.Usage.CompletionTokens))
			}
		}
	})
}
