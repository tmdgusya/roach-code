package cli

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"roach-code/internal/i18n"
)

// reasoningViewMax bounds the live thinking buffer the streamed block renders
// from. Re-rendering the full chain of thought on every delta was O(n²) (a 2k-
// token thought churned ~4.7GB); rendering only the trailing window keeps each
// delta O(1). The full text still lives in m.reasoning for verbose mode.
const reasoningViewMax = 4096

// reasoningTailLines caps how many trailing visual lines the LIVE thinking block
// shows. Kept small so the stream above the input stays calm (low motion); the
// full thought is retained and expandable via Ctrl+O, so showing little live loses
// nothing.
const reasoningTailLines = 4

// streamReasoning appends a chunk and rewrites the live reasoning block from a
// bounded trailing view (mirrors streamToolOutput), so the chain of thought is
// visible while the model works without re-rendering the whole thing per token.
func (m *chatTUI) streamReasoning(chunk string) {
	if m.reasoningTextIdx < 0 {
		return
	}
	m.reasoning.WriteString(chunk) // full text retained for verbose mode
	m.reasoningView = append(m.reasoningView, chunk...)
	if len(m.reasoningView) > reasoningViewMax {
		drop := len(m.reasoningView) - reasoningViewMax
		for drop < len(m.reasoningView) && !utf8.RuneStart(m.reasoningView[drop]) {
			drop++
		}
		m.reasoningView = m.reasoningView[:copy(m.reasoningView, m.reasoningView[drop:])]
	}
	m.transcript[m.reasoningTextIdx] = reasoningBlock(string(m.reasoningView), m.width, reasoningTailLines)
	m.transcriptDirty = true
}

// reasoningRail is the continuous left bar that runs down the live thinking
// block — every line (the "│ thinking…" marker and each reasoning line) shares
// it, so it reads as one aligned vertical rail rather than a marker glyph that
// fails to line up with a separate corner connector.
const reasoningRail = "  │ "

// reasoningBlock renders raw thinking text as dim, width-wrapped lines, each on
// the shared "│" rail so they align perfectly under the "│ thinking…" marker. A
// positive maxLines keeps only the trailing visual lines (the live view); 0
// renders all (verbose collapse).
func reasoningBlock(raw string, width, maxLines int) string {
	w := width - len([]rune(reasoningRail))
	if w < 8 {
		w = 8
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		for _, wl := range strings.Split(ansi.Wrap(expandTabs(ln), w, ""), "\n") {
			lines = append(lines, wl)
		}
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for i, ln := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(dim(reasoningRail + ln))
	}
	return b.String()
}

// committedThought records a finished reasoning block so collapse is
// non-destructive: markerIdx/textIdx point into m.transcript and text holds the
// full rationale, so /verbose (Ctrl+O) can expand or re-collapse it in place and a
// pending approval can reveal it without the reasoning ever being thrown away.
type committedThought struct {
	markerIdx int
	textIdx   int
	text      string
	secs      int
}

// reasoningApprovalLines bounds how many trailing thought lines an approval
// force-expands — enough to audit the intent behind the gated call without
// flooding the viewport above the banner.
const reasoningApprovalLines = 12

// renderThought (re)draws a committed reasoning block collapsed or expanded. The
// marker shows a "▸" (collapsed) / "▾" (expanded) disclosure triangle so the fold
// is discoverable; the text block below is blanked when collapsed and shown dim
// (trailing maxLines, 0 = all) when expanded. It rewrites in place — never splices
// — so stored thought indices stay valid for later toggles.
func (m *chatTUI) renderThought(t committedThought, expanded bool, maxLines int) {
	if t.markerIdx >= 0 && t.markerIdx < len(m.transcript) {
		tri := "▸"
		if expanded {
			tri = "▾"
		}
		m.transcript[t.markerIdx] = dim("  " + tri + " " + fmt.Sprintf(i18n.M.ChatThoughtForFmt, t.secs))
	}
	if t.textIdx >= 0 && t.textIdx < len(m.transcript) {
		if expanded {
			m.transcript[t.textIdx] = reasoningBlock(t.text, m.width, maxLines)
		} else {
			m.transcript[t.textIdx] = ""
		}
	}
}

// commitReasoning closes the live thinking block: the "▎ thinking…" marker is
// rewritten to a "▸/▾ thought for Ns" summary and the streamed text is RETAINED in
// m.thoughts (hidden when collapsed, never deleted) so it can be expanded later via
// Ctrl+O or surfaced during an approval. The viewport re-wraps from m.transcript,
// so the in-place change is flagged via transcriptDirty.
func (m *chatTUI) commitReasoning() {
	if m.reasoningLineIdx < 0 {
		return
	}
	secs := int(time.Since(m.thinkStart).Seconds())
	full := m.reasoning.String()
	if m.reasoningTextIdx >= 0 && strings.TrimSpace(full) != "" {
		t := committedThought{markerIdx: m.reasoningLineIdx, textIdx: m.reasoningTextIdx, text: full, secs: secs}
		m.thoughts = append(m.thoughts, t)
		m.renderThought(t, m.showReasoning, 0)
	} else {
		// Nothing was captured — leave a bare timer marker (no fold triangle, nothing
		// to expand) and blank any empty live block slot.
		m.transcript[m.reasoningLineIdx] = dim("  │ " + fmt.Sprintf(i18n.M.ChatThoughtForFmt, secs))
		if m.reasoningTextIdx >= 0 {
			m.transcript[m.reasoningTextIdx] = ""
		}
	}
	m.transcriptDirty = true
	m.reasoning.Reset()
	m.reasoningView = m.reasoningView[:0]
	m.reasoningLineIdx = -1
	m.reasoningTextIdx = -1
}

func (m *chatTUI) toggleVerboseReasoning(notify bool) {
	m.showReasoning = !m.showReasoning
	// Apply retroactively: re-render every committed thought in place so Ctrl+O
	// reveals or hides thoughts that already streamed, not just future ones.
	for _, t := range m.thoughts {
		m.renderThought(t, m.showReasoning, 0)
	}
	if len(m.thoughts) > 0 {
		m.transcriptDirty = true
	}
	if !notify {
		return
	}
	if m.showReasoning {
		m.notice("verbose on — thoughts shown (▾); Ctrl+O to collapse")
	} else {
		m.notice("verbose off — thoughts collapsed (▸); Ctrl+O to expand")
	}
}
