package agent

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// visibleWidth returns the column count of s after stripping ANSI SGR codes.
// Delegates to go-runewidth so emoji, fullwidth forms, and ZWJ sequences all
// measure correctly — a hand-rolled CJK-only table missed every emoji range
// and made the streamed-text row count drift on emoji-heavy answers.
func visibleWidth(s string) int {
	return runewidth.StringWidth(stripANSI(s))
}

// stripANSI copies s into a new string with ANSI SGR sequences removed.
// It walks bytes directly so no regexp is needed.
func stripANSI(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		if n := skipANSISGR(s); n > 0 {
			s = s[n:]
			continue
		}
		r, size := utf8.DecodeRuneInString(s)
		b.WriteRune(r)
		s = s[size:]
	}
	return b.String()
}

// skipANSISGR returns the byte length of an ANSI Select-Graphic-Rendition
// sequence (\e[…m) at the start of s, or 0 if s does not begin one.
func skipANSISGR(s string) int {
	if len(s) < 2 || s[0] != '\x1b' || s[1] != '[' {
		return 0
	}
	for i := 2; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || c == ';' {
			continue
		}
		if c == 'm' {
			return i + 1
		}
		break
	}
	return 0
}

// streamedRows counts how many rows the cursor has descended after raw text
// of length s was printed at the given terminal width. Used by the markdown
// redraw to know how far up to move before clearing. Each \n descends one
// row; lines whose visible width exceeds the terminal width descend an extra
// row per wrap. A line exactly the terminal width does not wrap on its own —
// terminals "lazy-wrap" only when the next visible character lands.
func streamedRows(s string, width int) int {
	if width <= 0 {
		width = 80
	}
	rows := 0
	for len(s) > 0 {
		// Extract one line (up to '\n'), stripping ANSI inline.
		var line strings.Builder
		hasNewline := false
		for len(s) > 0 {
			if n := skipANSISGR(s); n > 0 {
				s = s[n:]
				continue
			}
			r, size := utf8.DecodeRuneInString(s)
			s = s[size:]
			if r == '\n' {
				hasNewline = true
				break
			}
			line.WriteRune(r)
		}
		if w := runewidth.StringWidth(line.String()); w > 0 {
			rows += (w - 1) / width
		}
		if hasNewline {
			rows++ // count the '\n' itself
		}
	}
	return rows
}
