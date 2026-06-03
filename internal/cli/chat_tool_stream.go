package cli

import (
	"fmt"
	"strings"
	"time"

	"roach-code/internal/i18n"
)

// toolStreamTailLines caps how many trailing output lines a running tool shows;
// the live block scrolls within this window so a chatty build doesn't flood.
const toolStreamTailLines = 8

// resetToolStreamBuf clears the bounded-tail state of one tool run — the tail
// ring, the in-progress partial line, and the line counter. The slot identity
// (toolStreamIdx / toolStreamID) is managed at each call site, since begin /
// switch / collapse set it differently, so this touches only the three buffer
// fields and nothing else.
func (m *chatTUI) resetToolStreamBuf() {
	m.toolTail = m.toolTail[:0]
	m.toolPartial = ""
	m.toolLineCount = 0
}

// connectorLine renders a single dim gutter line ("╰─ …") under a tool card at
// the given width. It is the one-line form of the connector block; the
// multi-line streamToolOutput path builds its own slice and does not use it.
func connectorLine(text string, width int) string {
	return surfaceWrap(connectorBlock([]string{text}), width)
}

// streamToolOutput appends a chunk of a running tool's output and re-renders its
// live block (the last toolStreamTailLines lines) under the tool card, opening
// the block on the first chunk. Mirrors streamReasoning.
func (m *chatTUI) streamToolOutput(id, chunk string) {
	if id == "" {
		return
	}
	if m.toolStreamID != id {
		m.collapseToolOutput(m.toolStreamID)
		m.toolStreamID = id
		m.resetToolStreamBuf()
		m.toolStreamIdx = len(m.transcript)
		m.commitLine("")
	}
	// Fold completed lines into the bounded tail; keep the trailing partial.
	data := m.toolPartial + chunk
	for {
		i := strings.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		m.pushToolLine(strings.TrimRight(data[:i], "\r"))
		data = data[i+1:]
	}
	m.toolPartial = data

	vis := m.toolTail
	if m.toolPartial != "" {
		vis = append(append([]string{}, m.toolTail...), m.toolPartial)
	}
	lines := make([]string, len(vis))
	for i, ln := range vis {
		lines[i] = dim(clampPlain(ln, m.width-len([]rune(connector))))
	}
	m.transcript[m.toolStreamIdx] = surfaceWrap(connectorBlock(lines), m.width)
	m.transcriptDirty = true
}

// pushToolLine appends a completed output line to the bounded tail, dropping the
// oldest when it exceeds the window (the backing array stays ≤ window+1).
func (m *chatTUI) pushToolLine(line string) {
	m.toolLineCount++
	m.toolTail = append(m.toolTail, line)
	if len(m.toolTail) > toolStreamTailLines {
		copy(m.toolTail, m.toolTail[1:])
		m.toolTail = m.toolTail[:toolStreamTailLines]
	}
}

// collapseToolOutput replaces a finished tool's live block with a dim
// "╰─ N lines" summary, so the scrollback keeps a marker of the run without the
// full output (which the model already received). No-op when id isn't streaming.
func (m *chatTUI) collapseToolOutput(id string) {
	if m.toolStreamIdx < 0 || id == "" || m.toolStreamID != id {
		return
	}
	n := m.toolLineCount
	if m.toolPartial != "" {
		n++
	}
	if n == 0 {
		// The tool produced no streamed output (e.g. an MCP call like
		// codegraph_context) — drop the "working" placeholder so only the card
		// remains, rather than leaving a "0 lines" summary.
		if m.toolStreamIdx == len(m.transcript)-1 {
			m.transcript = m.transcript[:m.toolStreamIdx]
		} else {
			m.transcript[m.toolStreamIdx] = ""
		}
	} else {
		m.transcript[m.toolStreamIdx] = connectorLine(dim(fmt.Sprintf("%d lines", n)), m.width)
	}
	m.transcriptDirty = true
	m.toolStreamIdx = -1
	m.toolStreamID = ""
	m.resetToolStreamBuf()
}

// toolWorkingFrames is the braille spinner cycled once per second on the
// "╰─ working · Ns" line of a tool that hasn't streamed output yet.
var toolWorkingFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// beginToolRunning opens an empty live block under a just-dispatched tool card,
// keyed by the call id. tickToolRunning fills it with a "working · Ns" line each
// second; if the tool later streams output, streamToolOutput reuses the same
// block; collapseToolOutput closes it on the result.
func (m *chatTUI) beginToolRunning(id string) {
	if id == "" {
		return
	}
	m.toolStreamID = id
	m.resetToolStreamBuf()
	m.toolStreamStart = time.Now()
	m.toolStreamFrame = 0
	m.toolStreamIdx = len(m.transcript)
	m.commitLine(connectorLine(dim(fmt.Sprintf(i18n.M.ChatToolWorkingFmt, toolWorkingFrames[0], 0)), m.width))
}

// tickToolRunning re-renders the working line of a tool that's dispatched but
// hasn't produced output yet. A no-op once output streams in or no tool runs.
func (m *chatTUI) tickToolRunning() {
	if m.toolStreamIdx < 0 || m.toolLineCount != 0 || m.toolPartial != "" {
		return
	}
	m.toolStreamFrame++
	frame := toolWorkingFrames[m.toolStreamFrame%len(toolWorkingFrames)]
	secs := int(time.Since(m.toolStreamStart).Seconds())
	m.transcript[m.toolStreamIdx] = connectorLine(dim(fmt.Sprintf(i18n.M.ChatToolWorkingFmt, frame, secs)), m.width)
	m.transcriptDirty = true
}
