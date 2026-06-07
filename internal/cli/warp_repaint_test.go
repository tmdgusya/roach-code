package cli

import "testing"

// The Warp workaround forces a full repaint of the composer (tea.ClearScreen)
// instead of an incremental diff when a wide (CJK) glyph is present, because
// Warp corrupts in-place updates over wide glyphs. These tests cover the gate
// logic and wide-rune detection; the end-to-end render behavior is terminal-
// specific (Warp) and can't be exercised in CI.

func TestForceInputRepaintEnv(t *testing.T) {
	cases := []struct {
		name              string
		term, override    string
		wideNow, widePrev bool
		want              bool
	}{
		{"warp wide-now repaints", "WarpTerminal", "", true, false, true},
		{"warp wide-prev repaints (delete transition)", "WarpTerminal", "", false, true, true},
		{"warp ascii does not repaint", "WarpTerminal", "", false, false, false},
		{"non-warp never repaints", "WezTerm", "", true, true, false},
		{"override on forces repaint anywhere", "WezTerm", "1", false, false, true},
		{"override off disables on warp", "WarpTerminal", "0", true, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", c.term)
			t.Setenv("ROACH_FULL_REPAINT", c.override)
			if got := forceInputRepaint(c.wideNow, c.widePrev); got != c.want {
				t.Errorf("forceInputRepaint(%v,%v) = %v, want %v", c.wideNow, c.widePrev, got, c.want)
			}
		})
	}
}

func TestHasWideRune(t *testing.T) {
	for _, c := range []struct {
		s    string
		want bool
	}{
		{"abcd", false},
		{"", false},
		{"가", true},
		{"abc가d", true},
		{"ㅋㅋ", true},
		{"界", true},
	} {
		if got := hasWideRune(c.s); got != c.want {
			t.Errorf("hasWideRune(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}
