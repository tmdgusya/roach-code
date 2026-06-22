package cli

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// finalize drains the committed-line queue (its content is already mirrored in
// the transcript, which the viewport renders) and batches the turn's commands.
func finalize(m chatTUI, cmds []tea.Cmd) tea.Cmd {
	*m.pendingCommit = (*m.pendingCommit)[:0]
	return tea.Batch(cmds...)
}

// clampWidth hard-breaks any line wider than width so no scrollback line wraps
// in the terminal. bubbletea's inline renderer estimates how far to scroll for
// each printed block from each line's width (insertAbove: offset += width/w); an
// over-wide line that the terminal wraps throws that estimate off and drifts the
// pinned input box off-screen. Lines already within width are left byte-for-byte
// untouched (chunkByWidth preserves content and ANSI), so rendered tables and the
// wrapped answer — which the markdown renderer already fit to width — are safe;
// only stray long lines (tool-dispatch args, unwrapped code) get broken.
func clampWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	// ansi.Hardwrap breaks any line over `width` visible cols on grapheme
	// boundaries, preserving ANSI and counting wide chars — exactly what we want,
	// and lines already within width pass through unchanged.
	return ansi.Hardwrap(s, width, false)
}

// replaceTranscriptLine rewrites an already-committed transcript block in place
// and mirrors the change into pendingCommit when the block has not been flushed by
// finalize yet. In-place UI updates (thought folds, live output, read rollups)
// use transcriptDirty to force the viewport to re-feed without growing scrollback.
func (m *chatTUI) replaceTranscriptLine(idx int, s string) {
	if idx < 0 || idx >= len(m.transcript) {
		return
	}
	m.transcript[idx] = s
	if m.pendingCommit != nil {
		firstPending := len(m.transcript) - len(*m.pendingCommit)
		if idx >= firstPending && idx-firstPending < len(*m.pendingCommit) {
			(*m.pendingCommit)[idx-firstPending] = s
		}
	}
	m.transcriptDirty = true
}

func (m *chatTUI) clearReadRollup() {
	m.readRollupIdx = -1
	m.readRollupParent = ""
}

func (m *chatTUI) clearReadRollupFor(parentID string) {
	if m.readRollupParent == parentID {
		return
	}
	m.clearReadRollup()
}

// commitLine queues one finalized block for the next scrollback flush.
func (m *chatTUI) commitLine(s string) {
	*m.pendingCommit = append(*m.pendingCommit, s)
	m.transcript = append(m.transcript, s)
}

// commitSpacer separates the next block (a thinking marker or a tool line) from
// the previous one with a single blank line, skipping it at the top of the
// transcript or when a blank already trails so spacers never double up.
func (m *chatTUI) commitSpacer() {
	if n := len(m.transcript); n > 0 && strings.TrimSpace(m.transcript[n-1]) != "" {
		m.commitLine("")
	}
}

// panelRowCount is the number of terminal rows a rendered panel occupies: 0 when
// empty, otherwise its newline count + 1. It is the single source of the
// bottom-region row arithmetic shared by bottomRows and View (via appendPanel),
// so the viewport height budget and the layout that fills it can never drift.
func panelRowCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// appendPanel appends a non-empty rendered panel to parts and adds its row count
// to *rows — the per-panel step View repeats for each panel pinned above the
// input box.
func appendPanel(parts *[]string, rows *int, s string) {
	if s == "" {
		return
	}
	*parts = append(*parts, s)
	*rows += panelRowCount(s)
}

// bottomRows is the terminal-row height of the pinned bottom region: any open
// panels (todo / approval / chooser / rewind / resume-picker / jobs picker /
// completion), the input box (its line count plus top+bottom border), and the two
// fixed status rows. The panel set and order MUST match the appendPanel calls in
// View, so the transcript viewport (sized as height - bottomRows) leaves room for
// exactly what is drawn beneath it — when only View counted the resume picker the
// viewport ran one panel too tall while "/resume" was open.
func (m chatTUI) bottomRows() int {
	rows := panelRowCount(m.renderTodoPanel()) +
		panelRowCount(m.renderSubagentPanel()) +
		panelRowCount(m.renderApprovalBanner()) +
		panelRowCount(m.renderChooser()) +
		panelRowCount(m.renderRewind()) +
		panelRowCount(m.renderResumePicker()) +
		panelRowCount(m.renderJobsPicker()) +
		panelRowCount(m.renderCompletion())
	if m.state == tuiRunning {
		rows++ // the working spinner line above the box
	}
	return rows + m.input.Height() + 2 + 2
}

// transcriptHeight is the row budget left for the transcript viewport once the
// thread header and the pinned bottom region are accounted for (at least one row).
func (m chatTUI) transcriptHeight() int {
	if h := m.height - m.headerRows() - m.bottomRows(); h > 1 {
		return h
	}
	return 1
}
