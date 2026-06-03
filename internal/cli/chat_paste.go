package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// clampMiddle shortens s to fit w columns by eliding the MIDDLE ("head … tail"),
// so both a shell command's leading verb AND its trailing operators (&&, |, ; rm,
// curl) stay visible — the dangerous tail is never the part that gets cut. Plain
// text only (no ANSI); falls back to a tail-clip ellipsis when w is tiny.
func clampMiddle(s string, w int) string {
	if visibleWidth(s) <= w {
		return s
	}
	const sep = " … "
	budget := w - visibleWidth(sep)
	if budget < 2 {
		return clampPlain(s, w)
	}
	r := []rune(s)
	// Cut by DISPLAY COLUMNS, not rune count: accumulate the head until it fills
	// half the budget and the tail until it fills the rest, so wide/CJK glyphs can't
	// make head…tail overflow w (which would let the outer clamp eat the dangerous
	// trailing operators this whole function exists to preserve).
	headBudget := budget / 2
	hi, hw := 0, 0
	for hi < len(r) {
		cw := visibleWidth(string(r[hi]))
		if hw+cw > headBudget {
			break
		}
		hw += cw
		hi++
	}
	ti, tw, tailBudget := len(r), 0, budget-hw
	for ti > hi {
		cw := visibleWidth(string(r[ti-1]))
		if tw+cw > tailBudget {
			break
		}
		tw += cw
		ti--
	}
	if ti <= hi {
		return s
	}
	return string(r[:hi]) + sep + string(r[ti:])
}

// clampStatusLine truncates a status line to `width` visible columns, ANSI-aware,
// appending an ellipsis and a reset, then right-pads with spaces so the row
// reaches the full terminal width. The bottom region must stay a fixed height —
// the non-alt-screen renderer commits scrollback by clearing the prior frame's
// lines, so a status that wraps to a second row strands input-box borders in
// history. Truncating (not wrapping) keeps it one row regardless of how many tags
// (ctx · cache · avg · jobs · balance) it carries on a narrow terminal.
//
// The trailing space pad matters beyond aesthetics: when the line is shorter
// than the terminal width, the alt-screen keeps the prior frame's trailing
// cells in place on the next redraw. Without the pad, the right edge of the
// status row would bleed stale cells from the previous turn.
func clampStatusLine(s string, width int) string {
	if width <= 0 {
		return s
	}
	// ansi.Truncate is ANSI-aware, counts wide chars, and appends the tail when
	// it actually clips — one row regardless of how many tags the status carries.
	truncated := ansi.Truncate(s, width, "…")
	// Pad visible columns to `width`. We compute against the ANSI-stripped
	// length so escape codes (and the wide-glyph bookkeeping they imply) do
	// not throw the count off.
	pad := width - ansi.StringWidth(truncated)
	if pad > 0 {
		truncated += strings.Repeat(" ", pad)
	}
	return truncated
}

// growInputToFit resizes the textarea to the number of lines its value spans,
// capped at maxInputRows so a long paste doesn't crowd the screen.
const maxInputRows = 5
const foldedPasteMinChars = 1000
const foldedPasteMinLines = 5

type pastedBlock struct {
	label string
	text  string
	image bool // an image attachment: expands to its bare @ref, not a wrapped block
}

func pastedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"), "\n") + 1
}

func foldedPasteLabel(id, lines int) string {
	return fmt.Sprintf("[Pasted text #%d · %d lines]", id, lines)
}

func renderFoldedPasteBlock(block pastedBlock) string {
	return fmt.Sprintf("%s\n\n--- Begin %s ---\n%s\n--- End %s ---", block.label, block.label, block.text, block.label)
}

func shouldFoldPastedText(s string) bool {
	return len([]rune(s)) >= foldedPasteMinChars || pastedLineCount(s) >= foldedPasteMinLines
}

func (m *chatTUI) chooserTyping() bool {
	return m.chooser != nil && m.chooser.typing
}

func (m *chatTUI) shouldFoldPaste(s string) bool {
	return shouldFoldPastedText(s)
}

func (m *chatTUI) insertFoldedPaste(s string) {
	label := foldedPasteLabel(m.nextPasteID, pastedLineCount(s))
	m.nextPasteID++
	m.pastedBlocks = append(m.pastedBlocks, pastedBlock{label: label, text: s})
	m.input.InsertString(label + " ")
}

// insertImageRef puts a deletable [image #N] token in the input box (mapped to
// the saved attachment's @ref, expanded on submit) so a dragged/pasted image is
// edited and removed like any other text, not stranded in a separate tray.
func (m *chatTUI) insertImageRef(path string) {
	label := fmt.Sprintf("[image #%d]", m.nextPasteID)
	m.nextPasteID++
	m.pastedBlocks = append(m.pastedBlocks, pastedBlock{label: label, text: "@" + path, image: true})
	m.input.InsertString(label + " ")
	m.growInputToFit()
	m.updateCompletion()
}

func (m *chatTUI) expandPastedBlocks(displayed string) string {
	sent := displayed
	for _, block := range m.pastedBlocks {
		if !strings.Contains(sent, block.label) {
			continue
		}
		repl := renderFoldedPasteBlock(block)
		if block.image {
			repl = block.text
		}
		sent = strings.ReplaceAll(sent, block.label, repl)
	}
	return sent
}

func (m *chatTUI) pasteLabelsIn(s string) []string {
	var labels []string
	for _, block := range m.pastedBlocks {
		if strings.Contains(s, block.label) {
			labels = append(labels, block.label)
		}
	}
	return labels
}

func (m *chatTUI) clearSubmittedPastes() {
	if len(m.pendingPastes) == 0 {
		return
	}
	submitted := make(map[string]bool, len(m.pendingPastes))
	for _, label := range m.pendingPastes {
		submitted[label] = true
	}
	kept := m.pastedBlocks[:0]
	for _, block := range m.pastedBlocks {
		if !submitted[block.label] {
			kept = append(kept, block)
		}
	}
	m.pastedBlocks = kept
	m.pendingPastes = nil
}

// growInputToFit resizes the textarea to the number of visual lines its value
// spans, capped at maxInputRows so a long paste doesn't crowd the screen.
// When DynamicHeight is enabled the textarea already auto-sizes itself, but we
// keep this as a safety net for places that may need an explicit nudge.
func (m *chatTUI) growInputToFit() {
	if m.input.DynamicHeight {
		// The built-in recalculate already tracks visual lines; just ensure the
		// cap is applied. Calling SetHeight here would fight DynamicHeight, so
		// we let the textarea manage its own height.
		return
	}
	// Fallback: count hard newlines only (pre-DynamicHeight behaviour).
	lines := strings.Count(m.input.Value(), "\n") + 1
	if lines < 1 {
		lines = 1
	}
	if lines > maxInputRows {
		lines = maxInputRows
	}
	if lines != m.input.Height() {
		m.input.SetHeight(lines)
	}
}
