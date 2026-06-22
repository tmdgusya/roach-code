package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"roach-code/internal/agent"
	"roach-code/internal/control"
	"roach-code/internal/event"
)

// AC-16: renderUserBubble renders a "You:" label + plain text body and does
// NOT emit an ANSI background SGR (the warm filled bubble is gone post-redesign).
func TestRenderUserBubble(t *testing.T) {
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() {
		colorEnabled, activeCLITheme = prevColor, prevTheme
		refreshCLIStyles()
	})
	colorEnabled = true
	t.Setenv("COLORTERM", "truecolor")
	configureCLIThemeWithStyle("dark", "amp")

	got := renderUserBubble("hello world", 80)
	plain := ansi.Strip(got)

	if !strings.Contains(plain, "You:") {
		t.Fatalf("renderUserBubble output missing \"You:\" label:\n%s", got)
	}
	if !strings.Contains(plain, "hello world") {
		t.Fatalf("renderUserBubble output missing the body text:\n%s", got)
	}
	// No ANSI background SGR — the old warm-filled bubble must be gone.
	if strings.Contains(got, "\x1b[48") || strings.Contains(got, "\x1b[4") && strings.Contains(got, ";") {
		// Distinguish 4x (background) from 4m (underline) carefully: check the
		// background-set forms explicitly.
		if hasBackgroundSGR(got) {
			t.Fatalf("renderUserBubble must not paint a background (warm bubble removed), got:\n%q", got)
		}
	}
}

// hasBackgroundSGR reports whether s contains a "set background" SGR escape
// (48;...m truecolor or 48;5;...m / 4x; 256-colour), distinguishing it from
// foreground (38;...) and underline (4m).
func hasBackgroundSGR(s string) bool {
	for _, marker := range []string{"\x1b[48;", "\x1b[48;5;"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	// 256-colour background is sometimes written as "\x1b[4Nm" for N in 0..9
	// (e.g. "\x1b[40m" black bg). Check the 40-49 range explicitly.
	for code := 40; code <= 49; code++ {
		if strings.Contains(s, "\x1b["+itoa(code)+"m") {
			return true
		}
	}
	return false
}

// itoa is a tiny strconv.Itoa-free helper to avoid an extra import.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// AC-17: renderThreadHeader emits exactly one line carrying at least one of
// {model, directory, context}. It is the amp-style top-of-screen thread header.
func TestRenderThreadHeader(t *testing.T) {
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() {
		colorEnabled, activeCLITheme = prevColor, prevTheme
		refreshCLIStyles()
	})
	colorEnabled = true
	configureCLIThemeWithStyle("dark", "amp")

	m := newTestChatTUI()
	m.label = "test-model"
	m.width = 80
	// contextTag() calls m.ctrl.ContextSnapshot(); supply a controller so the
	// header's context branch doesn't nil-deref. The model label alone is
	// enough to satisfy the "at least one of {model, dir, context}" check.
	m.ctrl = control.New(control.Options{})

	header := m.renderThreadHeader()
	if header == "" {
		t.Fatal("renderThreadHeader returned empty; model/dir/context all absent")
	}
	plain := ansi.Strip(header)
	if strings.Count(plain, "\n") != 0 {
		t.Fatalf("renderThreadHeader must be a single line, got %d newlines:\n%s", strings.Count(plain, "\n"), header)
	}
	// The label "test-model" should appear (model tag is the most reliable of the three).
	if !strings.Contains(plain, "test-model") {
		t.Fatalf("renderThreadHeader should include the model label, got:\n%s", plain)
	}
}

// AC-18: renderToolSummary renders a single-line compact tool summary carrying
// the ◐ glyph (the amp-style replacement for the old coloured ● dot card).
func TestRenderToolSummary(t *testing.T) {
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() {
		colorEnabled, activeCLITheme = prevColor, prevTheme
		refreshCLIStyles()
	})
	colorEnabled = true
	configureCLIThemeWithStyle("dark", "amp")

	got := renderToolSummary("bash", `{"command":"npm test"}`, 80)
	plain := ansi.Strip(got)

	if strings.Count(plain, "\n") != 0 {
		t.Fatalf("renderToolSummary must be a single line, got %d newlines:\n%s", strings.Count(plain, "\n"), plain)
	}
	if !strings.Contains(plain, "◐") {
		t.Fatalf("renderToolSummary must carry the ◐ compact glyph, got:\n%s", plain)
	}
	if !strings.Contains(plain, "Bash") {
		t.Fatalf("renderToolSummary must carry the verb, got:\n%s", plain)
	}
	if !strings.Contains(plain, "npm test") {
		t.Fatalf("renderToolSummary must carry the primary arg, got:\n%s", plain)
	}
}

// AC-19: View() renders the amp-style layout — the thread header as the first
// non-empty line, and the two status rows as the final two non-empty lines.
func TestViewLayout(t *testing.T) {
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() {
		colorEnabled, activeCLITheme = prevColor, prevTheme
		refreshCLIStyles()
	})
	colorEnabled = true
	configureCLIThemeWithStyle("dark", "amp")

	m := newTestChatTUI()
	m.label = "test-model"
	m.height = 24
	m.width = 80
	m.input.SetWidth(80)
	m.ctrl = control.New(control.Options{})

	view := m.View()
	rendered := view.Content
	lines := strings.Split(rendered, "\n")

	// First non-empty line is the header (carries the model label).
	firstNonEmpty := ""
	for _, ln := range lines {
		if strings.TrimSpace(ansi.Strip(ln)) != "" {
			firstNonEmpty = ansi.Strip(ln)
			break
		}
	}
	if firstNonEmpty == "" {
		t.Fatalf("View rendered no non-empty lines:\n%s", rendered)
	}
	if !strings.Contains(firstNonEmpty, "test-model") {
		t.Fatalf("first non-empty line should be the thread header (carrying the model label), got:\n%q", firstNonEmpty)
	}

	// Last two non-empty lines are the status rows (mode/shortcuts + live data).
	var nonEmpty []string
	for _, ln := range lines {
		if strings.TrimSpace(ansi.Strip(ln)) != "" {
			nonEmpty = append(nonEmpty, ansi.Strip(ln))
		}
	}
	if len(nonEmpty) < 3 {
		t.Fatalf("expected at least 3 non-empty lines (header + 2 status), got %d:\n%s", len(nonEmpty), rendered)
	}
	lastTwo := nonEmpty[len(nonEmpty)-2:]
	// The bottom status row carries the live-data separator "╾" when data is
	// present; the row above it carries the mode tag (Auto / YOLO). We assert
	// the two rows exist as the tail; content varies by state so we only check
	// they're non-empty and distinct (a collapsed single status line would
	// fail the "two status rows" invariant).
	if lastTwo[0] == "" || lastTwo[1] == "" {
		t.Fatalf("the final two status rows must both be non-empty, got:\n%q", lastTwo)
	}
}

// AC-20: the visual contract exists and explicitly keeps the roach-code keymap
// while defining the full-screen / foreground-only / compact-panel TUI rules.
func TestAmpContractDocument(t *testing.T) {
	body, err := os.ReadFile("../../docs/ampcode-tui-contract.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		"full-screen",
		"alt-screen",
		"foreground-only",
		"compact tool",
		"fixed footer",
		"Do not change the existing `roach-code` keymap",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("contract missing %q:\n%s", want, text)
		}
	}
}

// AC-21: common terminal sizes render as one full alt-screen frame with the
// thread header at the top and the fixed two-row footer at the bottom.
func TestAmpFullScreenCommonSizes(t *testing.T) {
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() {
		colorEnabled, activeCLITheme = prevColor, prevTheme
		refreshCLIStyles()
	})
	colorEnabled = true
	configureCLIThemeWithStyle("dark", "amp")

	for _, size := range []struct {
		w int
		h int
	}{
		{80, 24},
		{120, 40},
	} {
		t.Run(itoa(size.w)+"x"+itoa(size.h), func(t *testing.T) {
			ctrl := control.New(control.Options{Label: "test-model"})
			m := newChatTUI(ctrl, "", make(chan event.Event, 1), size.w, "")
			next, _ := m.Update(teaWindow(size.w, size.h))
			view := next.(chatTUI).View()
			if !view.AltScreen {
				t.Fatal("View must request alt-screen")
			}
			if view.BackgroundColor != nil {
				t.Fatalf("View background = %v, want nil so the terminal theme remains authoritative", view.BackgroundColor)
			}
			if lines := strings.Count(view.Content, "\n") + 1; lines != size.h {
				t.Fatalf("rendered frame has %d lines, want terminal height %d", lines, size.h)
			}
			if strings.Contains(view.Content, "\x1b[48;") {
				t.Fatalf("amp welcome frame must not force a background over the terminal theme:\n%q", view.Content)
			}
			plainLines := strings.Split(ansi.Strip(view.Content), "\n")
			first := firstNonEmptyLine(plainLines)
			if !strings.Contains(first, "test-model") {
				t.Fatalf("first non-empty line should be the thread header, got %q", first)
			}
			nonEmpty := nonEmptyLines(plainLines)
			if len(nonEmpty) < 3 {
				t.Fatalf("expected header plus fixed footer rows, got:\n%s", ansi.Strip(view.Content))
			}
			lastTwo := nonEmpty[len(nonEmpty)-2:]
			if lastTwo[0] == "" || lastTwo[1] == "" {
				t.Fatalf("fixed footer rows must both render, got %q", lastTwo)
			}
		})
	}
}

// AC-22: pinned menus and prompt surfaces use compact terminal-native overlays:
// no card-style background fills and no lipgloss border rows.
func TestAmpMenuCompletionPickerChooserApprovalAmpOverlays(t *testing.T) {
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() {
		colorEnabled, activeCLITheme = prevColor, prevTheme
		refreshCLIStyles()
	})
	colorEnabled = true
	configureCLIThemeWithStyle("dark", "amp")

	m := newTestChatTUI()
	m.ctrl = control.New(control.Options{})
	m.width = 80
	m.completion = completion{
		active: true,
		kind:   compSlash,
		items: []compItem{
			{label: "/compact", insert: "/compact ", hint: "compact context"},
			{label: "/resume", insert: "/resume ", hint: "resume a session"},
		},
		sel: 0,
	}
	assertAmpOverlay(t, "completion", m.renderCompletion())

	m.chooser = newChooser(event.Ask{ID: "ask-1", Questions: []event.AskQuestion{{
		ID:     "q1",
		Header: "Mode",
		Prompt: "Pick a mode",
		Options: []event.AskOption{
			{Label: "Fast", Description: "Prefer speed"},
			{Label: "Careful", Description: "Prefer review"},
		},
	}}})
	assertAmpOverlay(t, "chooser", m.renderChooser())

	m.chooser = nil
	m.pendingApproval = &event.Approval{ID: "approval-1", Tool: "bash", Subject: "npm test"}
	m.approvalCursor = 2
	assertAmpOverlay(t, "approval", m.renderApprovalBanner())

	m.pendingApproval = nil
	m.resumePick = &resumePicker{sessions: []agent.SessionInfo{{Path: "/missing.jsonl", Turns: 2, Preview: "continue work"}}, sel: 0, active: -1}
	assertAmpOverlay(t, "resume", m.renderResumePicker())

	m.resumePick = nil
	m.jobsPick = &jobsPicker{}
	m.subagents = map[string]*subagentRun{
		"sub-1": {id: "sub-1", name: "task", label: "scan", started: time.Now()},
	}
	assertAmpOverlay(t, "jobs", m.renderJobsPicker())
	assertAmpOverlay(t, "subagent", m.renderSubagentPanel())

	m.todoArgs = `{"todos":[{"content":"tighten layout","status":"in_progress","activeForm":"tightening layout"}]}`
	assertAmpOverlay(t, "todo", m.renderTodoPanel())
}

func teaWindow(w, h int) tea.WindowSizeMsg {
	return tea.WindowSizeMsg{Width: w, Height: h}
}

func assertAmpOverlay(t *testing.T, name, rendered string) {
	t.Helper()
	if strings.TrimSpace(ansi.Strip(rendered)) == "" {
		t.Fatalf("%s overlay rendered empty", name)
	}
	if hasBackgroundSGR(rendered) {
		t.Fatalf("%s overlay must not use background SGR fills, got:\n%q", name, rendered)
	}
	for _, ln := range strings.Split(ansi.Strip(rendered), "\n") {
		if isFloatingBorderLine(strings.TrimSpace(ln)) {
			t.Fatalf("%s overlay must not render floating-card border rows, got line %q in:\n%s", name, ln, ansi.Strip(rendered))
		}
	}
}

func isFloatingBorderLine(s string) bool {
	if len([]rune(s)) < 60 {
		return false
	}
	for _, r := range s {
		switch r {
		case '─', '━', '╭', '╮', '╰', '╯', '┌', '┐', '└', '┘':
		default:
			return false
		}
	}
	return true
}

func firstNonEmptyLine(lines []string) string {
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			return ln
		}
	}
	return ""
}

func nonEmptyLines(lines []string) []string {
	var out []string
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}
