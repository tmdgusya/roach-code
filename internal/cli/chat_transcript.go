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

// bottomRows is the terminal-row height of the pinned bottom region: any open
// panels (todo / approval / chooser / rewind / completion), the input box (its
// line count plus top+bottom border), and the two fixed status rows.
func (m chatTUI) bottomRows() int {
	rows := 0
	for _, s := range []string{
		m.renderTodoPanel(),
		m.renderApprovalBanner(),
		m.renderChooser(),
		m.renderRewind(),
		m.renderCompletion(),
	} {
		if s != "" {
			rows += strings.Count(s, "\n") + 1
		}
	}
	if m.state == tuiRunning {
		rows++ // the working spinner line above the box
	}
	return rows + m.input.Height() + 2 + 2
}

// transcriptHeight is the row budget left for the transcript viewport once the
// pinned bottom region is accounted for (at least one row).
func (m chatTUI) transcriptHeight() int {
	if h := m.height - m.bottomRows(); h > 1 {
		return h
	}
	return 1
}
