package textarea

import (
	"strings"
	"testing"

	"github.com/rivo/uniseg"
)

// TestWrapNoOverflow guards the roach-code soft-wrap fix: every visual line that
// wrap() produces must be at most `width` cells wide. Upstream dumped a whole
// no-space word onto one line, so a double-width glyph that straddled the wrap
// column overflowed and was clipped by the viewport (the trailing character
// vanished from the input box). See the "roach-code fix" note in wrap().
func TestWrapNoOverflow(t *testing.T) {
	cases := []struct {
		in    string
		width int
	}{
		{"가나다라마X바", 12}, // the reported bug: 바 used to vanish
		{"가나다라마바사", 12}, // pure-wide, boundary lands evenly (was already OK)
		{"abcdefghijklm", 12},
		{"가나다라마바", 12}, // fills the line exactly, cursor wraps
		{"a가b나c다d라", 8},
		{"가X나Y다Z라W마", 6},
		{"한글입력테스트중간삽입", 10},
		{"xxxxx가나다라마바사아자", 9},
	}
	for _, c := range cases {
		lines := wrap([]rune(c.in), c.width)
		for i, ln := range lines {
			if w := uniseg.StringWidth(string(ln)); w > c.width {
				t.Errorf("wrap(%q, %d) line[%d]=%q width %d exceeds %d",
					c.in, c.width, i, string(ln), w, c.width)
			}
		}
		// No rune may be lost: concatenating the wrapped lines and stripping the
		// soft-wrap padding spaces must recover the original runes.
		var sb strings.Builder
		for _, ln := range lines {
			sb.WriteString(string(ln))
		}
		joined := strings.ReplaceAll(sb.String(), " ", "")
		want := strings.ReplaceAll(c.in, " ", "")
		if joined != want {
			t.Errorf("wrap(%q, %d) lost/garbled runes: got %q want %q",
				c.in, c.width, joined, want)
		}
	}
}

// TestWrapMidInsertViaView reproduces the original symptom through the public
// API: type a wide-char line, move the cursor back, insert — the inserted-after
// content must remain visible (here the final glyph wrapped to the next line).
func TestWrapMidInsertViaView(t *testing.T) {
	ti := New()
	ti.Prompt = ""
	ti.ShowLineNumbers = false
	ti.DynamicHeight = true
	ti.MaxHeight = 6
	ti.SetWidth(12)
	ti.SetValue("가나다라마X바")

	view := ti.View()
	// strip ANSI then collapse to visible runes across all wrapped rows
	plain := stripSGR(view)
	flat := strings.ReplaceAll(strings.ReplaceAll(plain, "\n", ""), " ", "")
	if !strings.Contains(flat, "바") {
		t.Fatalf("inserted-after glyph 바 missing from view; flattened=%q", flat)
	}
	if !strings.Contains(flat, "가나다라마X바") {
		t.Fatalf("view does not contain the full value; flattened=%q", flat)
	}
}

// stripSGR removes CSI sequences for the assertion above (kept local so this
// test has no extra deps).
func stripSGR(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\x1b' && i+1 < len(rs) && rs[i+1] == '[' {
			i += 2
			for i < len(rs) && !(rs[i] >= '@' && rs[i] <= '~') {
				i++
			}
			continue
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}
