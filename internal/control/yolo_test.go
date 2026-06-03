package control

import (
	"context"
	"testing"
	"time"

	"roach-code/internal/event"
)

func TestRequestApprovalHonorsBypass(t *testing.T) {
	var approvalRequested bool
	c := New(Options{
		Sink: event.FuncSink(func(e event.Event) {
			if e.Kind == event.ApprovalRequest {
				approvalRequested = true
			}
		}),
	})
	c.SetBypass(true)

	done := make(chan bool, 1)
	go func() {
		allow, _, err := c.requestApproval(context.Background(), "bash", "echo hi")
		if err != nil {
			t.Errorf("requestApproval: %v", err)
		}
		done <- allow
	}()

	select {
	case allow := <-done:
		if !allow {
			t.Fatal("bypass should auto-allow the approval")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("requestApproval blocked under bypass; it must auto-allow without prompting")
	}

	if approvalRequested {
		t.Fatal("bypass must not emit an ApprovalRequest event")
	}
}
