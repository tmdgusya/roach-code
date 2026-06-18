package cli

import (
	"strings"
	"testing"

	"roach-code/internal/control"
	"roach-code/internal/event"
)

// TestViewRunningStatusLineTPS verifies that the status line during a running
// turn shows TPS when turnTokens and elapsed are both positive, and omits it
// when either is zero.
func TestViewRunningStatusLineTPS(t *testing.T) {
	ctrl := control.New(control.Options{Sink: event.Discard, SessionDir: t.TempDir(), Label: "test"})

	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() { colorEnabled, activeCLITheme = prevColor, prevTheme })
	colorEnabled = true

	cases := []struct {
		name       string
		turnTokens int
		elapsed    int
		wantTPS    bool // whether "@ N/s" or "@ X.XK/s" pattern should be present
		wantTok    bool // whether the "↓" token readout should be present
	}{
		{
			name:       "positive tps shown",
			turnTokens: 3500,
			elapsed:    10,
			wantTPS:    true,
			wantTok:    true,
		},
		{
			name:       "elapsed zero omits tps",
			turnTokens: 3500,
			elapsed:    0,
			wantTPS:    false,
			wantTok:    true,
		},
		{
			name:       "turnTokens zero omits everything",
			turnTokens: 0,
			elapsed:    10,
			wantTPS:    false,
			wantTok:    false,
		},
		{
			name:       "very slow (<1 tps) omits tps",
			turnTokens: 5,
			elapsed:    10,
			wantTPS:    false,
			wantTok:    true,
		},
		{
			name:       "hundreds of tps",
			turnTokens: 120000,
			elapsed:    10,
			wantTPS:    true,
			wantTok:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestChatTUI()
			m.ctrl = ctrl
			m.state = tuiRunning
			m.turnTokens = tc.turnTokens
			m.elapsed = tc.elapsed
			// The View() function also uses shimmerPhase for the star glyph;
			// any non-negative value works.
			m.shimmerPhase = 0

			view := m.View().Content

			// Check token readout.
			hasTok := strings.Contains(view, "↓")
			if hasTok != tc.wantTok {
				t.Fatalf("View() contains ↓ = %v, want %v\nView output snippet:\n%s",
					hasTok, tc.wantTok, extractStatusLine(view))
			}

			// Check TPS pattern: "@ N/s" or "@ X.XK/s".
			hasTPS := containsTPS(view)
			if hasTPS != tc.wantTPS {
				t.Fatalf("View() contains TPS = %v, want %v (turnTokens=%d, elapsed=%d)\nView output snippet:\n%s",
					hasTPS, tc.wantTPS, tc.turnTokens, tc.elapsed, extractStatusLine(view))
			}
		})
	}
}

// containsTPS reports whether the view contains a TPS pattern like "@ 3.5K/s" or "@ 42/s".
func containsTPS(view string) bool {
	for i := 0; i < len(view); i++ {
		if view[i] == '@' {
			rest := view[i:]
			if strings.Contains(rest, "/s") {
				return true
			}
		}
	}
	return false
}

// extractStatusLine returns just the first few lines containing the status info
// for diagnostic output.
func extractStatusLine(view string) string {
	lines := strings.Split(view, "\n")
	for _, line := range lines {
		if strings.Contains(line, "↓") || strings.Contains(line, "@") || strings.Contains(line, "thinking") {
			return line
		}
	}
	if len(lines) > 5 {
		return strings.Join(lines[:5], "\n")
	}
	return strings.Join(lines, "\n")
}

// TestViewIdleNoTPS verifies that when the TUI is not in running state, no
// TPS or token readout appears in the status area.
func TestViewIdleNoTPS(t *testing.T) {
	ctrl := control.New(control.Options{Sink: event.Discard, SessionDir: t.TempDir(), Label: "test"})
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() { colorEnabled, activeCLITheme = prevColor, prevTheme })
	colorEnabled = true

	m := newTestChatTUI()
	m.ctrl = ctrl
	m.state = tuiIdle
	m.turnTokens = 5000
	m.elapsed = 10

	view := m.View().Content
	if containsTPS(view) {
		t.Fatalf("View() should not contain TPS when idle, found @ pattern in:\n%s",
			extractStatusLine(view))
	}
}

// TestViewRunningTPSScale verifies the formatted TPS value at different scales.
func TestViewRunningTPSScale(t *testing.T) {
	ctrl := control.New(control.Options{Sink: event.Discard, SessionDir: t.TempDir(), Label: "test"})
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() { colorEnabled, activeCLITheme = prevColor, prevTheme })
	colorEnabled = true

	cases := []struct {
		name       string
		turnTokens int
		elapsed    int
		wantSubstr string
	}{
		{
			name:       "low tps plain number",
			turnTokens: 123,
			elapsed:    10,
			wantSubstr: "@12/s",
		},
		{
			name:       "kilo tps with decimal",
			turnTokens: 35000,
			elapsed:    10,
			wantSubstr: "@3.5K/s",
		},
		{
			name:       "tens of kilo tps",
			turnTokens: 120000,
			elapsed:    10,
			wantSubstr: "@12.0K/s",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestChatTUI()
			m.ctrl = ctrl
			m.state = tuiRunning
			m.turnTokens = tc.turnTokens
			m.elapsed = tc.elapsed
			m.shimmerPhase = 0

			view := m.View().Content
			if !strings.Contains(view, tc.wantSubstr) {
				t.Fatalf("View() should contain %q for turnTokens=%d elapsed=%d\nView snippet:\n%s",
					tc.wantSubstr, tc.turnTokens, tc.elapsed, extractStatusLine(view))
			}
		})
	}
}

// TestViewRunningDisplayFormat verifies the exact display format of the status line.
func TestViewRunningDisplayFormat(t *testing.T) {
	ctrl := control.New(control.Options{Sink: event.Discard, SessionDir: t.TempDir(), Label: "test"})
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() { colorEnabled, activeCLITheme = prevColor, prevTheme })
	colorEnabled = true

	m := newTestChatTUI()
	m.ctrl = ctrl
	m.state = tuiRunning
	m.turnTokens = 42000
	m.elapsed = 12
	m.shimmerPhase = 0

	view := m.View().Content
	want := "↓42K @3.5K/s"
	if !strings.Contains(view, want) {
		t.Fatalf("View() should contain %q, got:\n%s", want, extractStatusLine(view))
	}
}
