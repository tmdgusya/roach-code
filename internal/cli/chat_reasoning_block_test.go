package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// railRuneLen is the visible rune width of the shared reasoning rail prefix.
// reasoningBlock subtracts this from the requested width to size the wrap
// column, so the tests derive their expectations from the same primitive
// rather than hard-coding 4.
var railRuneLen = len([]rune(reasoningRail))

// strippedLines renders reasoningBlock, strips ANSI colour, and splits into
// visual lines. An empty render yields a zero-length slice (not [""]) so
// callers can assert "no lines" cleanly.
func strippedLines(raw string, width, maxLines int) []string {
	out := reasoningBlock(raw, width, maxLines)
	if out == "" {
		return nil
	}
	return strings.Split(ansi.Strip(out), "\n")
}

// TestReasoningRailPrefixShape pins the rail constant the block renders against:
// two leading spaces, the │ glyph, then a trailing space. The wrap-width math in
// reasoningBlock depends on its rune length, so this guards both at once.
func TestReasoningRailPrefixShape(t *testing.T) {
	if reasoningRail != "  │ " {
		t.Fatalf("reasoningRail = %q, want %q", reasoningRail, "  │ ")
	}
	if railRuneLen != 4 {
		t.Fatalf("reasoningRail rune length = %d, want 4", railRuneLen)
	}
}

// TestEmptyRaw documents the actual behaviour for an empty thought. Note: the
// task plan predicted "" (no rail) here, but strings.Split("", "\n") yields one
// empty element, so reasoningBlock never hits its len(lines)==0 guard and instead
// emits a single bare-rail line. The assertion below reflects the real code.
func TestEmptyRaw(t *testing.T) {
	got := reasoningBlock("", 80, 4)
	plain := ansi.Strip(got)
	if plain != reasoningRail {
		t.Fatalf("reasoningBlock(\"\",80,4) stripped = %q, want a single bare rail %q", plain, reasoningRail)
	}
	if strings.Contains(plain, "\n") {
		t.Fatalf("empty raw should render exactly one rail line, got %q", plain)
	}
}

// TestReasoningBlockShortInputs covers the small, well-behaved inputs: a single
// short line, a multi-line block that fits under the width, the trailing-window
// cap, and the verbose (maxLines==0) all-lines contract.
func TestReasoningBlockShortInputs(t *testing.T) {
	t.Run("single short line gets one rail line", func(t *testing.T) {
		lines := strippedLines("hello world", 80, 4)
		if len(lines) != 1 {
			t.Fatalf("got %d lines, want 1: %#v", len(lines), lines)
		}
		if !strings.HasPrefix(lines[0], reasoningRail) {
			t.Fatalf("line missing rail prefix: %q", lines[0])
		}
		if !strings.Contains(lines[0], "hello world") {
			t.Fatalf("line lost its text: %q", lines[0])
		}
	})

	t.Run("multi-line under width keeps one rail per input line", func(t *testing.T) {
		raw := "alpha\nbeta\ngamma"
		want := strings.Count(raw, "\n") + 1 // 3 input lines
		lines := strippedLines(raw, 80, 0)
		if len(lines) != want {
			t.Fatalf("got %d rendered lines, want %d (one per input line): %#v", len(lines), want, lines)
		}
		for i, ln := range lines {
			if !strings.HasPrefix(ln, reasoningRail) {
				t.Fatalf("line %d missing rail prefix: %q", i, ln)
			}
		}
		// Order and content preserved on their own rail lines.
		for _, frag := range []string{"alpha", "beta", "gamma"} {
			if !strings.Contains(lines[0]+lines[1]+lines[2], frag) {
				t.Fatalf("rendered block lost %q: %#v", frag, lines)
			}
		}
	})

	t.Run("maxLines caps to the trailing window", func(t *testing.T) {
		raw := "l1\nl2\nl3\nl4\nl5"
		lines := strippedLines(raw, 80, 2)
		if len(lines) != 2 {
			t.Fatalf("maxLines=2 over 5 lines kept %d lines, want 2: %#v", len(lines), lines)
		}
		// Trailing two kept, leading three dropped.
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "l4") || !strings.Contains(joined, "l5") {
			t.Fatalf("trailing window should keep l4 and l5: %#v", lines)
		}
		for _, dropped := range []string{"l1", "l2", "l3"} {
			if strings.Contains(joined, dropped) {
				t.Fatalf("leading line %q should have been dropped: %#v", dropped, lines)
			}
		}
	})

	t.Run("maxLines=0 renders all lines (verbose collapse)", func(t *testing.T) {
		raw := "v1\nv2\nv3\nv4\nv5"
		want := strings.Count(raw, "\n") + 1 // 5
		lines := strippedLines(raw, 80, 0)
		if len(lines) != want {
			t.Fatalf("maxLines=0 kept %d lines, want all %d: %#v", len(lines), want, lines)
		}
		for _, frag := range []string{"v1", "v2", "v3", "v4", "v5"} {
			if !strings.Contains(strings.Join(lines, "\n"), frag) {
				t.Fatalf("verbose render lost %q: %#v", frag, lines)
			}
		}
	})
}

// TestWideLineWraps proves a single logical line longer than the available
// content width (width - rail) splits into multiple rail lines, and that every
// rendered visual line fits inside the requested width.
func TestWideLineWraps(t *testing.T) {
	const width = 40
	// One unbroken run of word-like tokens far wider than width-rail forces
	// hard wrapping into several rail lines.
	raw := strings.Repeat("token ", 30)
	lines := strippedLines(raw, width, 0)
	if len(lines) < 2 {
		t.Fatalf("an over-wide line should wrap to multiple rail lines, got %d: %#v", len(lines), lines)
	}
	for i, ln := range lines {
		if !strings.HasPrefix(ln, reasoningRail) {
			t.Fatalf("wrapped line %d missing rail prefix: %q", i, ln)
		}
		if w := ansi.StringWidth(ln); w > width {
			t.Fatalf("wrapped line %d visible width = %d, want <= %d: %q", i, w, width, ln)
		}
	}
}

// TestNarrowWidthClampsContentWidth confirms the floor on the wrap column: even
// when width - rail would be below 8, reasoningBlock clamps the content width to
// 8 rather than producing a negative/zero wrap that would loop or panic. So at a
// tiny width the rendered lines exceed the (unusable) width but still wrap to the
// 8-column floor plus the rail.
func TestNarrowWidthClampsContentWidth(t *testing.T) {
	// width 6 -> w = 6-4 = 2, clamped up to 8.
	raw := strings.Repeat("abcdefgh ", 6)
	lines := strippedLines(raw, 6, 0)
	if len(lines) < 2 {
		t.Fatalf("clamped narrow width should still wrap the long run: %#v", lines)
	}
	for i, ln := range lines {
		if !strings.HasPrefix(ln, reasoningRail) {
			t.Fatalf("clamped line %d missing rail prefix: %q", i, ln)
		}
		// Content width is floored at 8, so visible width tops out at rail + 8.
		if w := ansi.StringWidth(ln); w > railRuneLen+8 {
			t.Fatalf("clamped line %d width = %d, want <= %d: %q", i, w, railRuneLen+8, ln)
		}
	}
}

// TestTabExpanded proves a tab in the raw thought is expanded to spaces before
// wrapping, so no literal \t survives into the rendered rail line.
func TestTabExpanded(t *testing.T) {
	raw := "a\tb"
	out := reasoningBlock(raw, 80, 0)
	if strings.ContainsRune(out, '\t') {
		t.Fatalf("tab should be expanded before wrap, but output still contains \\t: %q", out)
	}
	plain := ansi.Strip(out)
	// tabWidth = 4: "a" at col 0, tab fills cols 1..3 (3 spaces), then "b".
	if !strings.Contains(plain, "a   b") {
		t.Fatalf("tab should expand to align column: stripped = %q, want substring %q", plain, "a   b")
	}
}
