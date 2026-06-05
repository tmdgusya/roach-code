package agent

import (
	"testing"

	"roach-code/internal/event"
	"roach-code/internal/provider"
)

func TestSubSinkForForwardsUsageWithParentID(t *testing.T) {
	var got []event.Event
	sink := subSinkFor("task-1", event.FuncSink(func(e event.Event) {
		got = append(got, e)
	}))

	sink.Emit(event.Event{Kind: event.Usage, Usage: &provider.Usage{TotalTokens: 123}})

	if len(got) != 1 {
		t.Fatalf("forwarded events = %d, want 1", len(got))
	}
	if got[0].Kind != event.Usage || got[0].ParentID != "task-1" || got[0].Usage.TotalTokens != 123 {
		t.Fatalf("usage event not forwarded with parent id: %+v", got[0])
	}
}

func TestSubSinkForStillNestsToolEvents(t *testing.T) {
	var got event.Event
	sink := subSinkFor("task-1", event.FuncSink(func(e event.Event) {
		got = e
	}))

	sink.Emit(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "read-1", Name: "read_file"}})

	if got.Tool.ParentID != "task-1" || got.Tool.ID != "task-1/read-1" {
		t.Fatalf("tool event not nested under parent: %+v", got.Tool)
	}
}
