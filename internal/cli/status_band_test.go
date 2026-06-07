package cli

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"roach-code/internal/control"
	"roach-code/internal/event"
)

// TestStatusRowHasNoSurfaceBackgroundBand is a regression guard for the
// "black band under the input box" bug: the bottom status block used to be
// painted with a `surface` Background that lipgloss stretched across the
// full terminal width, so any cell the status text did not reach was
// filled with a near-black panel colour. In the dark theme `surface`
// (#1b1410, luma 234) is one step away from the page `ink` (#15100d, luma
// 233), so the unfilled cells read as a black band. The fix is to leave
// the status style's Background unset — every status row should now
// contain NO `48;` (background) SGR at all, and the visible width should
// still reach boxW thanks to clampStatusLine's right-pad.
func TestStatusRowHasNoSurfaceBackgroundBand(t *testing.T) {
	defer func(prev bool) { colorEnabled = prev }(colorEnabled)
	colorEnabled = true
	refreshCLIStyles()

	const boxW = 80
	ctrl := control.New(control.Options{})
	m := newChatTUI(ctrl, "", make(chan event.Event, 1), boxW, "")
	next, _ := m.Update(tea.WindowSizeMsg{Width: boxW, Height: 24})
	rendered := next.(chatTUI).View().Content

	plain := bottomStatusPlain(rendered)
	lines := strings.Split(plain, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected 2 status lines, got %d:\n%s", len(lines), plain)
	}
	for i, line := range lines {
		if got := ansi.Strip(line); len([]rune(got)) != boxW {
			t.Errorf("status line %d visible width = %d, want %d (raw=%q)", i, len([]rune(got)), boxW, got)
		}
	}

	// No `48;` (background) SGR anywhere in the rendered status block —
	// the band should be gone. The 48;5;234 sequence specifically is the
	// surface colour from the bug, so it MUST be absent; we also assert no
	// background SGR at all to catch any future "let me paint a slightly
	// different surface here" regression.
	for i, line := range lines {
		if strings.Contains(line, "\x1b[48;5;234m") {
			t.Errorf("status line %d still carries the surface (48;5;234) band: %q", i, line)
		}
		if strings.Contains(line, "\x1b[48;") {
			t.Errorf("status line %d has an unexpected background SGR: %q", i, line)
		}
	}
}

// TestClampStatusLinePadsToWidth pins clampStatusLine's right-pad behaviour:
// short inputs must be padded with spaces so the alt-screen never bleeds
// stale trailing cells from the previous frame. The fix relies on this
// pad to keep the status row visually clean after dropping the surface
// Background.
func TestClampStatusLinePadsToWidth(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		width int
	}{
		{"shorter than width", "  Auto │ ready", 40},
		{"empty", "", 20},
		{"exactly width", "0123456789", 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clampStatusLine(tc.in, tc.width)
			if w := ansi.StringWidth(got); w != tc.width {
				t.Fatalf("clampStatusLine(%q, %d) visible width = %d, want %d", tc.in, tc.width, w, tc.width)
			}
		})
	}
}
