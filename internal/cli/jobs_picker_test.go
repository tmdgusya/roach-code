package cli

import (
	"context"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"roach-code/internal/control"
	"roach-code/internal/event"
	"roach-code/internal/jobs"
	"roach-code/internal/provider"
)

func TestJobsPickerOpensAndShowsRetainedJobOutput(t *testing.T) {
	jm := jobs.NewManager(event.Discard)
	done := jm.Start("task", "scan", func(context.Context, io.Writer) (string, error) {
		return "final answer", nil
	})
	_ = jm.Wait(context.Background(), []string{done.ID}, 0)

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Jobs: jm})
	m.width = 80

	m.runSlashCommand("/jobs")
	if m.jobsPick == nil {
		t.Fatal("bare /jobs should open the interactive jobs picker")
	}
	plain := ansi.Strip(m.renderJobsPicker())
	if !strings.Contains(plain, "Background tasks") || !strings.Contains(plain, done.ID+" (scan) · task · done") {
		t.Fatalf("jobs picker should list retained terminal jobs:\n%s", plain)
	}

	next, _ := m.handleJobsPickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(chatTUI)
	plain = ansi.Strip(m.renderJobsPicker())
	if !strings.Contains(plain, "final answer") {
		t.Fatalf("Enter should inspect selected job output:\n%s", plain)
	}
}

func TestJobsPickerKillsSelectedRunningJob(t *testing.T) {
	jm := jobs.NewManager(event.Discard)
	running := jm.Start("task", "scan", func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Jobs: jm})
	m.width = 80
	m.openJobsPicker()

	next, _ := m.handleJobsPickerKey(tea.KeyPressMsg{Code: 'x'})
	m = next.(chatTUI)
	_ = jm.Wait(context.Background(), []string{running.ID}, 0)
	plain := ansi.Strip(m.renderJobsPicker())
	if !strings.Contains(plain, "Killed background job") || !strings.Contains(plain, running.ID+" (scan) · task · killed") {
		t.Fatalf("x should kill selected running job and keep it visible:\n%s", plain)
	}
}

func TestJobsPickerEscDismisses(t *testing.T) {
	jm := jobs.NewManager(event.Discard)
	done := jm.Start("task", "scan", func(context.Context, io.Writer) (string, error) {
		return "final answer", nil
	})
	_ = jm.Wait(context.Background(), []string{done.ID}, 0)

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Jobs: jm})
	m.openJobsPicker()
	if m.jobsPick == nil {
		t.Fatal("jobs picker should open")
	}

	next, _ := m.handleJobsPickerKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = next.(chatTUI)
	if m.jobsPick != nil {
		t.Fatal("Esc should close the jobs picker")
	}
}

func TestJobsPickerOpensFromDownOnEmptyComposer(t *testing.T) {
	jm := jobs.NewManager(event.Discard)
	done := jm.Start("task", "scan", func(context.Context, io.Writer) (string, error) {
		return "final answer", nil
	})
	_ = jm.Wait(context.Background(), []string{done.ID}, 0)

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{Jobs: jm})
	m.input.Reset()

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(chatTUI)
	if m.jobsPick == nil {
		t.Fatal("Down on an empty composer with jobs should open the jobs picker")
	}
}

func TestJobsPickerShowsForegroundSubagentDetail(t *testing.T) {
	m := newTestChatTUI()
	m.width = 100
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
		ID: "task1", Name: "task",
		Args: `{"description":"scan auth","prompt":"find auth callsites"}`,
	}})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
		ID: "task1/read1", ParentID: "task1", Name: "read_file", Args: `{"path":"auth.go"}`,
	}})
	m.ingestEvent(event.Event{Kind: event.Usage, ParentID: "task1", Usage: &provider.Usage{TotalTokens: 2400}})

	m.openJobsPicker()
	if m.jobsPick == nil {
		t.Fatal("live subagent should open the tasks picker")
	}
	next, _ := m.handleJobsPickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(chatTUI)
	plain := ansi.Strip(m.renderJobsPicker())
	for _, want := range []string{
		"task1 (scan auth) · task · running",
		"tools: 1",
		"tokens: 2K",
		"activity: Read auth.go",
		"prompt: find auth callsites",
		"press x twice",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("foreground subagent detail missing %q:\n%s", want, plain)
		}
	}
}

func TestJobsPickerStopsForegroundSubagentWithConfirm(t *testing.T) {
	m := newTestChatTUI()
	ctrl := control.New(control.Options{})
	m.ctrl = ctrl
	m.width = 100
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
		ID: "task1", Name: "task", Args: `{"description":"scan auth","prompt":"find auth callsites"}`,
	}})
	m.openJobsPicker()

	next, _ := m.handleJobsPickerKey(tea.KeyPressMsg{Code: 'x'})
	m = next.(chatTUI)
	plain := ansi.Strip(m.renderJobsPicker())
	if !strings.Contains(plain, "Press x again") {
		t.Fatalf("first x should arm foreground stop:\n%s", plain)
	}
	next, _ = m.handleJobsPickerKey(tea.KeyPressMsg{Code: 'x'})
	m = next.(chatTUI)
	plain = ansi.Strip(m.renderJobsPicker())
	if !strings.Contains(plain, "Stopped foreground subagent") {
		t.Fatalf("second x should stop foreground subagent:\n%s", plain)
	}
}
