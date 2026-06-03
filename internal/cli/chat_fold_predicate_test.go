package cli

import (
	"strings"
	"testing"
)

// TestPastedLineCount pins the newline-counting helper that drives the fold
// predicate and the "[Pasted text #N · M lines]" label. An empty string is
// zero lines (no block at all); a single line with no terminator is one line;
// and CRLF / bare-CR sequences are normalised to LF before counting, so a
// Windows or classic-Mac paste reports the same line total a Unix one would.
func TestPastedLineCount(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"single-line-no-newline", "abc", 1},
		{"two-lines-lf", "a\nb", 2},
		{"crlf-and-bare-cr-both-counted", "a\r\nb\rc", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pastedLineCount(tc.in); got != tc.want {
				t.Fatalf("pastedLineCount(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestShouldFoldPastedText exercises both fold triggers at their exact
// boundaries: a paste folds once it reaches foldedPasteMinLines lines OR
// foldedPasteMinChars runes. Line and char thresholds are independent, the
// comparisons are inclusive (>=), and the char count is measured in RUNES not
// bytes — so 1000 multibyte CJK characters fold even though one short line of
// them is far over 1000 bytes.
func TestShouldFoldPastedText(t *testing.T) {
	// Guard the assumed thresholds so this test fails loudly if the consts move.
	if foldedPasteMinLines != 5 {
		t.Fatalf("foldedPasteMinLines = %d, test assumes 5", foldedPasteMinLines)
	}
	if foldedPasteMinChars != 1000 {
		t.Fatalf("foldedPasteMinChars = %d, test assumes 1000", foldedPasteMinChars)
	}

	fourLines := strings.Repeat("a\n", foldedPasteMinLines-2) + "a" // 4 lines
	fiveLines := strings.Repeat("a\n", foldedPasteMinLines-1) + "a" // 5 lines

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"four-lines-short", fourLines, false},
		{"five-lines-folds-on-line-boundary", fiveLines, true},
		{"single-line-999-runes", strings.Repeat("x", foldedPasteMinChars-1), false},
		{"single-line-1000-runes", strings.Repeat("x", foldedPasteMinChars), true},
		{"999-cjk-runes-counted-by-rune", strings.Repeat("가", foldedPasteMinChars-1), false},
		{"1000-cjk-runes-counted-by-rune", strings.Repeat("가", foldedPasteMinChars), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldFoldPastedText(tc.in); got != tc.want {
				t.Fatalf("shouldFoldPastedText(len=%d runes) = %v, want %v",
					len([]rune(tc.in)), got, tc.want)
			}
		})
	}
}

// TestPastedLineCountIsRuneSafetyNetForFold sanity-checks the boundary line
// counts the fold predicate relies on, independent of shouldFoldPastedText, so
// a regression in pastedLineCount that happened to still trip the char trigger
// could not hide a broken line trigger.
func TestPastedLineCountIsRuneSafetyNetForFold(t *testing.T) {
	fourLines := strings.Repeat("a\n", foldedPasteMinLines-2) + "a"
	fiveLines := strings.Repeat("a\n", foldedPasteMinLines-1) + "a"
	if got := pastedLineCount(fourLines); got != foldedPasteMinLines-1 {
		t.Fatalf("pastedLineCount(fourLines) = %d, want %d", got, foldedPasteMinLines-1)
	}
	if got := pastedLineCount(fiveLines); got != foldedPasteMinLines {
		t.Fatalf("pastedLineCount(fiveLines) = %d, want %d", got, foldedPasteMinLines)
	}
}

// TestFoldedPasteLabel pins the exact label format used both as the placeholder
// dropped into the composer and as the fence header in the expanded block. The
// id and line count interpolate in order with a middot separator and a trailing
// " lines]" — any drift here would desync placeholder matching from rendering.
func TestFoldedPasteLabel(t *testing.T) {
	cases := []struct {
		name  string
		id    int
		lines int
		want  string
	}{
		{"id1-6-lines", 1, 6, "[Pasted text #1 · 6 lines]"},
		{"id3-5-lines", 3, 5, "[Pasted text #3 · 5 lines]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := foldedPasteLabel(tc.id, tc.lines); got != tc.want {
				t.Fatalf("foldedPasteLabel(%d, %d) = %q, want %q", tc.id, tc.lines, got, tc.want)
			}
		})
	}
}

// TestRenderFoldedPasteBlock proves the expansion wraps the verbatim pasted
// text between "--- Begin <label> ---" and "--- End <label> ---" fences, with
// the bare label as a heading on its own line above a blank line. The label
// therefore appears exactly three times (heading, begin fence, end fence) and
// the original text survives byte-for-byte between the fences.
func TestRenderFoldedPasteBlock(t *testing.T) {
	label := foldedPasteLabel(2, 7)
	const body = "line one\nline two\nline three"
	block := pastedBlock{label: label, text: body}

	got := renderFoldedPasteBlock(block)

	// Rebuild the expected string from the same primitives the source uses so
	// the assertion is an exact, recomputed baseline rather than a guess.
	want := label + "\n\n--- Begin " + label + " ---\n" + body + "\n--- End " + label + " ---"
	if got != want {
		t.Fatalf("renderFoldedPasteBlock mismatch:\n got: %q\nwant: %q", got, want)
	}

	if n := strings.Count(got, label); n != 3 {
		t.Fatalf("label %q should appear 3 times (heading + begin + end), got %d in %q", label, n, got)
	}
	if !strings.Contains(got, "--- Begin "+label+" ---") {
		t.Fatalf("missing begin fence in %q", got)
	}
	if !strings.Contains(got, "--- End "+label+" ---") {
		t.Fatalf("missing end fence in %q", got)
	}
	if !strings.Contains(got, body) {
		t.Fatalf("verbatim body not preserved inside fences: %q", got)
	}
}
