package cli

import (
	"strings"

	"roach-code/internal/control"
)

// rewriteAnswerBlock overwrites the open streamed answer block in place, shared
// by streamAnswer and commitPending. It is the PLAIN transcript write — unlike
// replaceTranscriptLine it deliberately does NOT mirror the value into
// *pendingCommit, because the answer slot's pendingCommit handling differs; only
// the in-place transcript[answerIdx] write + transcriptDirty re-feed is shared.
func (m *chatTUI) rewriteAnswerBlock(block string) {
	m.transcript[m.answerIdx] = block
	m.transcriptDirty = true
}

// streamAnswer renders the answer streamed so far up to its last completed
// paragraph (flushableMarkdownPrefix) and writes it as one transcript block,
// rewritten in place as later paragraphs land — so a long reply appears chunk by
// chunk instead of all at once on turn end. The trailing, still-streaming block
// stays buffered (a half-written fence/list never renders early), and it only
// re-renders when a new paragraph actually closes.
func (m *chatTUI) streamAnswer() {
	prefix := flushableMarkdownPrefix(m.pending.String())
	if len(prefix) <= m.answerFlushed {
		return
	}
	rendered := m.renderer.Render(prefix)
	if rendered == "" {
		return
	}
	m.answerFlushed = len(prefix)
	block := strings.TrimRight(rendered, "\n")
	if m.answerIdx < 0 {
		m.answerIdx = len(m.transcript)
		m.commitLine(block)
	} else {
		m.rewriteAnswerBlock(block)
	}
}

// commitPending freezes the full accumulated answer as markdown — overwriting the
// streamed block if one is open (streamAnswer), else committing fresh. Joining
// commitReasoning then commitPending puts the answer on its own line, restoring
// the thinking→answer break the renderer strips.
func (m *chatTUI) commitPending() {
	if m.pending.Len() == 0 {
		m.answerIdx = -1
		m.answerFlushed = 0
		return
	}
	// Drop a trailing GOAL_STATUS sentinel from the DISPLAYED answer — the goal loop
	// reads the verdict from the raw session message (keeping the cache-stable prefix
	// intact), so it must never reach the visible transcript.
	raw := control.StripGoalVerdict(m.pending.String())
	if raw == "" {
		m.pending.Reset()
		m.answerIdx = -1
		m.answerFlushed = 0
		return
	}
	rendered := m.renderer.Render(raw)
	if rendered == "" {
		rendered = raw
	}
	block := strings.TrimRight(rendered, "\n")
	if m.answerIdx < 0 {
		m.commitLine(block)
	} else {
		m.rewriteAnswerBlock(block)
	}
	m.pending.Reset()
	m.answerIdx = -1
	m.answerFlushed = 0
}

// flushableMarkdownPrefix returns the longest prefix of buf made of complete
// markdown blocks — text up to the last blank line outside any open fenced code
// block. A blank line inside a ``` / ~~~ fence isn't a boundary, so a half-written
// code block stays buffered until it closes.
func flushableMarkdownPrefix(buf string) string {
	lines := strings.Split(buf, "\n")
	inFence := false
	boundary := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if !inFence && t == "" {
			boundary = i
		}
	}
	if boundary <= 0 {
		return ""
	}
	return strings.Join(lines[:boundary], "\n")
}
