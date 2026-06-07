package cli

import (
	"context"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"roach-code/internal/control"
	"roach-code/internal/event"
	"roach-code/internal/i18n"
	"roach-code/internal/jobs"
)

// TestRunStatuslineCmd checks the custom status-line runner: it returns the
// first stdout line and forwards the JSON payload on stdin.
func TestRunStatuslineCmd(t *testing.T) {
	t.Setenv("ROACH_STATUSLINE_HELPER", "1")

	// Multi-line output collapses to the first row.
	if got := runStatuslineCmd(statuslineHelperCommand("multiline"), "{}"); got != "row-one" {
		t.Errorf("multi-line output should collapse to the first row, got %q", got)
	}
	// The JSON payload is delivered on stdin.
	if got := runStatuslineCmd(statuslineHelperCommand("stdin"), `{"model":"deepseek"}`); got != `{"model":"deepseek"}` {
		t.Errorf("stdin payload not forwarded, got %q", got)
	}
	// A failing command yields an empty line, not an error.
	if got := runStatuslineCmd(statuslineHelperCommand("fail"), "{}"); got != "" {
		t.Errorf("failed command should yield empty, got %q", got)
	}
}

func TestStatuslineHelperProcess(t *testing.T) {
	if os.Getenv("ROACH_STATUSLINE_HELPER") != "1" {
		return
	}
	mode := ""
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
			break
		}
	}
	switch mode {
	case "multiline":
		_, _ = os.Stdout.WriteString("row-one\nrow-two\n")
	case "stdin":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "fail":
		os.Exit(3)
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
	os.Exit(0)
}

func statuslineHelperCommand(mode string) string {
	if runtime.GOOS == "windows" {
		return os.Args[0] + " -test.run=TestStatuslineHelperProcess -- " + mode
	}
	return shellQuote(os.Args[0]) + " -test.run=TestStatuslineHelperProcess -- " + mode
}

func shellQuote(s string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// TestRunStatuslineDisabled confirms no command means no work (nil cmd), without
// touching the controller.
func TestRunStatuslineDisabled(t *testing.T) {
	m := chatTUI{} // no statuslineCmd, nil ctrl
	if cmd := m.runStatusline(); cmd != nil {
		t.Error("an unconfigured status line must return a nil tea.Cmd")
	}
}

func TestIdleStatuslineContainsModeAndReady(t *testing.T) {
	i18n.DetectLanguage("en")

	content := renderStatuslineView(t, false)
	plain := bottomStatusPlain(content)
	if !strings.Contains(plain, "Auto") {
		t.Fatalf("idle status line missing Auto mode:\n%s", plain)
	}
	if !strings.Contains(plain, "ready") {
		t.Fatalf("idle status line missing ready state:\n%s", plain)
	}
	for _, old := range []string{"Shift-Tab", "Ctrl-O", "Ctrl-D", "Enter sends", "Esc clears/exits state", "PgUp/PgDn"} {
		if strings.Contains(plain, old) {
			t.Fatalf("idle status line should not contain %q:\n%s", old, plain)
		}
	}
	if strings.Contains(plain, "[auto]") {
		t.Fatalf("idle status line should use Auto label, not bracketed tag:\n%s", plain)
	}
}

func TestYoloStatuslineUsesDangerPill(t *testing.T) {
	i18n.DetectLanguage("en")

	// With colour enabled, YOLO wears the solid danger-red pill.
	defer func(prev bool) { colorEnabled = prev }(colorEnabled)
	colorEnabled = true
	content := renderStatuslineView(t, true)
	plain := bottomStatusPlain(content)
	if !strings.Contains(plain, "YOLO") || !strings.Contains(plain, "approvals skipped") {
		t.Fatalf("YOLO status line missing warning text:\n%s", plain)
	}
	if !strings.Contains(content, "\x1b[48;2;229;72;77m") {
		t.Fatalf("YOLO status line should use desktop danger red background, got:\n%q", content)
	}

	// Under NO_COLOR / a non-tty the red field is gone, so YOLO must still shout in
	// text — skip-all-approvals can never read as a quiet, unemphasised word.
	colorEnabled = false
	plain = bottomStatusPlain(renderStatuslineView(t, true))
	if !strings.Contains(plain, "[!YOLO]") {
		t.Fatalf("NO_COLOR YOLO should fall back to a text amplifier:\n%s", plain)
	}
}

func TestStatuslineShowsEffort(t *testing.T) {
	i18n.DetectLanguage("en")

	content := renderStatuslineViewWithEffort(t, "auto")
	plain := bottomStatusPlain(content)
	if !strings.Contains(plain, "deepseek-v4-flash · effort auto") {
		t.Fatalf("status data line should show effort:\n%s", plain)
	}
}

func TestStatuslineExplicitEffortUsesAccentColor(t *testing.T) {
	i18n.DetectLanguage("en")

	defer func(prev bool) { colorEnabled = prev }(colorEnabled)
	colorEnabled = true
	// Re-initialise styles so the accent color registers are populated.
	refreshCLIStyles()

	content := renderStatuslineViewWithEffort(t, "max")
	plain := bottomStatusPlain(content)
	if !strings.Contains(plain, "effort max") {
		t.Fatalf("status data line should show explicit effort:\n%s", plain)
	}
	// Explicit effort uses the active theme's accent color (copper-coral #d97757 for graphite).
	if !strings.Contains(content, "\x1b[38;2;217;119;87m") && !strings.Contains(content, "\x1b[38;5;173m") {
		t.Fatalf("explicit effort should use graphite accent color, got:\n%q", content)
	}
}

func TestStatuslineShowsRunningAndRetainedDoneJobs(t *testing.T) {
	i18n.DetectLanguage("en")

	jm := jobs.NewManager(event.Discard)
	running := jm.Start("task", "scan", func(ctx context.Context, _ io.Writer) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	done := jm.Start("task", "done", func(context.Context, io.Writer) (string, error) {
		return "answer", nil
	})
	_ = jm.Wait(context.Background(), []string{done.ID}, 0)

	ctrl := control.New(control.Options{Jobs: jm})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80, "")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	plain := bottomStatusPlain(next.(chatTUI).View().Content)
	if !strings.Contains(plain, "1 job") {
		t.Fatalf("running jobs should take priority in status line:\n%s", plain)
	}
	if strings.Contains(plain, "done") {
		t.Fatalf("running jobs should not be mixed with terminal done count:\n%s", plain)
	}

	if !jm.Kill(running.ID) {
		t.Fatal("expected running job to be killable")
	}
	_ = jm.Wait(context.Background(), []string{running.ID}, 0)
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	plain = bottomStatusPlain(next.(chatTUI).View().Content)
	if !strings.Contains(plain, "2 jobs done") {
		t.Fatalf("retained terminal jobs should remain visible in status line:\n%s", plain)
	}
}

func TestRefreshEffortStatusUsesCurrentModel(t *testing.T) {
	isolateUserConfig(t)

	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80, "")
	m.modelRef = "deepseek-flash/deepseek-v4-flash"
	m.refreshEffortStatus()
	if m.effortLevel != "auto" {
		t.Fatalf("effortLevel = %q, want auto", m.effortLevel)
	}
}

func renderStatuslineView(t *testing.T, yolo bool) string {
	t.Helper()

	ctrl := control.New(control.Options{})
	ctrl.SetBypass(yolo)
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80, "")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(chatTUI).View().Content
}

func renderStatuslineViewWithEffort(t *testing.T, effort string) string {
	t.Helper()

	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), 80, "")
	m.label = "deepseek-v4-flash"
	m.effortLevel = effort
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(chatTUI).View().Content
}

func bottomStatusPlain(content string) string {
	lines := strings.Split(ansi.Strip(content), "\n")
	if len(lines) < 2 {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-2:], "\n")
}
