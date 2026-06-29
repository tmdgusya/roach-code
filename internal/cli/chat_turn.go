package cli

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"roach-code/internal/event"
	"roach-code/internal/i18n"
	"roach-code/internal/provider"
)

// cycleMode toggles normal ↔ YOLO (Shift+Tab). YOLO auto-approves every tool
// call for the session (deny rules still apply). The status line's mode tag
// reflects the result.
func (m *chatTUI) cycleMode() {
	m.ctrl.SetBypass(!m.ctrl.Bypass())
}

// startTurn commits the user bubble to scrollback, resets the turn accumulator,
// and kicks off the controller turn. `sent` goes to the model uncomposed;
// `displayed` is what the transcript shows, and `restore` is what Esc puts back
// while the bubble is still deferred.
func (m *chatTUI) startTurn(sent, displayed, restore string) tea.Cmd {
	return m.startTurnWithRaw(sent, displayed, restore, sent)
}

// startTurnWithRaw is startTurn plus an explicit `raw` typed input for callers
// that resolve @-references before sending the model input.
func (m *chatTUI) startTurnWithRaw(sent, displayed, restore, raw string) tea.Cmd {
	return m.startMessageTurnWithRaw(provider.Message{Role: provider.RoleUser, Content: sent}, displayed, restore, raw)
}

func (m *chatTUI) startMessageTurnWithRaw(msg provider.Message, displayed, restore, raw string) tea.Cmd {
	// Flush any half-streamed leftover before the new turn (defensive).
	m.commitReasoning()
	m.commitPending()

	// The live welcome glow is a fresh-screen flourish: the instant the conversation
	// begins, freeze it — commit the static banner to the top of the transcript so it
	// becomes normal scrollback and the viewport takes over from the live render.
	if m.bannerLive {
		m.bannerLive = false
		staticBanner := strings.TrimRight(renderTUIBanner(m.label, m.missing, m.updateNotice, m.width), "\n")
		m.transcript = append([]string{staticBanner}, m.transcript...)
		m.transcriptDirty = true
	}

	// Echo the user bubble to scrollback now so it appears the instant Enter is
	// pressed, not when the server's first packet lands. It stays un-sendable until
	// then: Esc before the reply pops these lines back off (unsendPending) and
	// restores the text to the input box, leaving nothing stranded.
	m.pendingRestore = restore
	m.pendingPastes = m.pasteLabelsIn(restore)
	m.bubbleStartIdx = len(m.transcript)
	m.commitLine("") // blank line separating turns
	m.commitLine(renderUserBubble(displayed, m.width))
	m.bubblePending = true
	m.turnDiscarded = false

	m.state = tuiRunning
	m.runStart = time.Now()
	m.elapsed = 0
	m.turnTokens = 0
	// The controller owns the run goroutine, its context, and cancellation; it
	// streams events to eventCh and emits TurnDone when the turn settles.
	m.ctrl.SendMessageWithRaw(msg, raw)
	return tea.Batch(m.spinner.Tick, elapsedTick())
}

// confirmBubbleSent marks the already-echoed user bubble as really sent once a
// turn's first response packet arrives, so Esc no longer un-sends it (it cancels
// the stream instead). Also called defensively at turn end. A no-op once confirmed.
func (m *chatTUI) confirmBubbleSent() {
	if !m.bubblePending {
		return
	}
	m.bubblePending = false
	m.pendingRestore = ""
}

// unsendPending "un-sends" the in-flight turn while the server hasn't replied yet
// (bubblePending): it pops the echoed bubble back off the transcript, restores the
// just-sent text to the input box, and cancels the request — marking the turn
// discarded so its already-buffered events reach nothing. Once a packet has arrived
// the bubble is confirmed and this path isn't taken (Esc cancels normally instead).
func (m *chatTUI) unsendPending() {
	m.input.SetValue(m.pendingRestore)
	m.growInputToFit()
	m.transcript = m.transcript[:m.bubbleStartIdx]
	m.transcriptDirty = true
	// Drop any committed thoughts whose transcript slots were just truncated away,
	// so a later Ctrl+O / expand can't address a stale index past the new tail.
	for len(m.thoughts) > 0 && m.thoughts[len(m.thoughts)-1].markerIdx >= m.bubbleStartIdx {
		m.thoughts = m.thoughts[:len(m.thoughts)-1]
	}
	if m.approvalExpandedThought >= len(m.thoughts) {
		m.approvalExpandedThought = -1
	}
	m.bubblePending = false
	m.pendingRestore = ""
	m.pendingPastes = nil
	m.turnDiscarded = true
	m.ctrl.Cancel()
}

// ingestEvent routes one typed event from the agent. Reasoning (dim) and answer
// free-text accumulate in their live buffers; every other event first finalizes
// the reasoning and answer streamed so far, then commits its own line —
// preserving order. Switching on the event Kind replaces the old prefix-sniffing
// of a flattened byte stream: the structure is now explicit.
func (m *chatTUI) ingestEvent(e event.Event) {
	if m.turnDiscarded {
		// The turn was un-sent (Esc before any packet); swallow whatever was already
		// buffered for it until it settles, so nothing lands in scrollback.
		if e.Kind == event.TurnDone {
			m.turnDiscarded = false
			m.state = tuiIdle
			m.ultragoalActive = false
		}
		return
	}
	// The first packet of any kind means the server replied — confirm the send so
	// Esc cancels the stream instead of un-sending. TurnStarted is local (emitted
	// before the request) and TurnDone is handled in its own case.
	if e.Kind != event.TurnStarted && e.Kind != event.TurnDone {
		m.confirmBubbleSent()
	}
	switch e.Kind {
	case event.TurnStarted:
		m.clearReadRollup()

	case event.Reasoning:
		m.clearReadRollup()
		if m.reasoningLineIdx < 0 {
			// Show the marker plus a live text block the moment thinking starts; the
			// text streams in below it and the block collapses to "thought for Ns"
			// when it closes (kept expanded only in verbose mode).
			m.commitSpacer()
			m.thinkStart = time.Now()
			m.reasoningLineIdx = len(m.transcript)
			m.commitLine(dim("  │ " + i18n.M.ChatThinking))
			m.reasoningTextIdx = len(m.transcript)
			m.commitLine("")
			m.reasoningView = m.reasoningView[:0]
		}
		m.streamReasoning(e.Text)

	case event.Text:
		m.clearReadRollup()
		m.commitReasoning() // reasoning ends as the answer begins
		m.pending.WriteString(e.Text)
		m.streamAnswer()

	case event.Message:
		m.clearReadRollup()
		// The answer stream is complete — freeze reasoning + the markdown answer.
		m.commitReasoning()
		m.commitPending()

	case event.ToolDispatch:
		// The early (partial) dispatch only carries the name — the full dispatch
		// with args prints the line. The running spinner covers the gap meanwhile.
		if e.Tool.Partial {
			break
		}
		m.finalizeStreamed()
		if e.Tool.ParentID != "" {
			m.renderSubagentChildDispatch(e.Tool)
			break
		}
		switch e.Tool.Name {
		case "todo_write":
			m.clearReadRollup()
			// Drive the pinned task list above the input (renderTodoPanel) rather
			// than printing a tool line; it updates in place as the list evolves.
			m.todoArgs = e.Tool.Args
		case "read_file":
			line := surfaceWrap(toolCard(e.Tool.Name, e.Tool.Args, m.width), m.width)
			if m.readRollupIdx >= 0 && m.readRollupParent == "" && m.readRollupIdx < len(m.transcript) {
				m.replaceTranscriptLine(m.readRollupIdx, line)
			} else {
				m.commitSpacer()
				m.readRollupIdx = len(m.transcript)
				m.readRollupParent = ""
				m.commitLine(line)
			}
			m.beginToolRunning(e.Tool.ID)
		default:
			m.clearReadRollup()
			m.commitSpacer()
			if block := diffBlock(e.Tool.Name, e.Tool.Args, e.Tool.FileDiff, m.width, diffScrollbackMaxLines); block != nil {
				for _, ln := range block {
					m.commitLine(surfaceWrap(ln, m.width))
				}
				break
			}
			m.commitLine(surfaceWrap(toolCard(e.Tool.Name, e.Tool.Args, m.width), m.width))
			m.rememberSubagentDispatch(e.Tool)
			m.beginToolRunning(e.Tool.ID)
		}

	case event.ToolProgress:
		m.streamToolOutput(e.Tool.ID, e.Tool.Output)

	case event.ToolResult:
		// A successful result is silent (it only feeds the model); a blocked/failed
		// call surfaces a red "◐ Verb ⊘ <reason>" line. A live-output block (bash)
		// collapses to a one-line "╰─ N lines" summary first.
		m.collapseToolOutput(e.Tool.ID)
		if e.Tool.ParentID != "" {
			if e.Tool.Err != "" {
				m.clearReadRollup()
				m.finalizeStreamed()
				m.commitLine(surfaceWrap("  │ "+red("◐")+" "+bold(toolDisplayName(e.Tool.Name))+" "+red("⊘ "+e.Tool.Err), m.width))
			}
			break
		}
		if isSubagentRootTool(e.Tool.Name) {
			m.clearReadRollup()
			m.finalizeStreamed()
			m.summarizeSubagentResult(e.Tool)
			break
		}
		if e.Tool.Err != "" {
			m.clearReadRollup()
			m.finalizeStreamed()
			m.commitLine(surfaceWrap("  "+red("◐")+" "+bold(toolDisplayName(e.Tool.Name))+" "+red("⊘ "+e.Tool.Err), m.width))
		}

	case event.Usage:
		m.clearReadRollup()
		if e.ParentID != "" {
			if e.Usage != nil {
				m.recordSubagentUsage(e.ParentID, e.Usage.TotalTokens)
			}
			break
		}
		if e.Usage != nil {
			m.turnTokens += e.Usage.CompletionTokens
		}
		tps := 0
		if m.elapsed > 0 && m.turnTokens > 0 {
			tps = m.turnTokens / m.elapsed
		}
		if line := usageLine(e.Usage, e.Pricing, tps); line != "" {
			m.finalizeStreamed()
			m.commitLine(line)
		}

	case event.Notice:
		m.clearReadRollup()
		glyph := "·"
		if e.Level == event.LevelWarn {
			glyph = "!"
		}
		m.finalizeStreamed()
		m.commitLine(fmt.Sprintf("  %s %s", glyph, e.Text))

	case event.CompactionStarted:
		m.clearReadRollup()
		m.finalizeStreamed()
		m.commitLine(dim("  ⋯ " + i18n.M.CompactionWorking))

	case event.CompactionDone:
		m.clearReadRollup()
		// An aborted pass carries no summary; the accompanying Notice (auto) or
		// compactDoneMsg error (manual) explains why, so don't draw an empty card.
		if e.Compaction.Summary == "" {
			break
		}
		m.finalizeStreamed()
		for _, ln := range compactionCardLines(e.Compaction) {
			m.commitLine(ln)
		}

	case event.Phase:
		m.clearReadRollup()
		m.finalizeStreamed()
		m.commitLine(fmt.Sprintf("[%s]", e.Text))

	case event.ApprovalRequest:
		m.clearReadRollup()
		// The controller's run goroutine is now blocked inside the gate awaiting
		// this decision; the banner shows it in View and key input answers it via
		// ctrl.Approve. At most one prompt is outstanding (the controller
		// serialises them), so a plain field holds the current one.
		a := e.Approval
		m.pendingApproval = &a
		m.pendingApprovalParent = e.ParentID
		m.markSubagentAwaitingApproval(e.ParentID, a)
		// Highlight Deny by default for destructive calls (so a reflexive Enter
		// denies); benign reads and the plan gate default to Allow-once.
		m.approvalCursor = 2
		if !approvalDestructive(a.Tool) {
			m.approvalCursor = 0
		}
		// Keep the rationale for THIS gated call on screen during the decision:
		// force-expand the most recent thought (bounded) unless verbose already
		// shows everything. Restored to the verbose setting when the gate resolves.
		m.approvalExpandedThought = -1
		if n := len(m.thoughts); n > 0 && !m.showReasoning {
			m.renderThought(m.thoughts[n-1], true, reasoningApprovalLines)
			m.approvalExpandedThought = n - 1
			m.transcriptDirty = true
		}

	case event.AskRequest:
		m.clearReadRollup()
		// The `ask` tool raised a question card; the run goroutine blocks until
		// ctrl.AnswerQuestion resolves it. Keys drive the card while it's set.
		m.finalizeStreamed()
		m.chooser = newChooser(e.Ask)

	case event.TurnDone:
		m.clearReadRollup()
		// The turn settled — freeze anything still streaming and surface a real error.
		// Autosave already happened in Controller so every frontend shares the same
		// activity-time semantics.
		m.commitReasoning()
		m.commitPending()
		// The bubble was echoed on Enter and an un-sent turn is swallowed above
		// (turnDiscarded), so any turn reaching here keeps its bubble in scrollback;
		// just clear the un-sendable flag.
		m.confirmBubbleSent()
		m.state = tuiIdle
		m.ultragoalActive = false
		m.clearSubmittedPastes()
		// Sweep any subagent rows still in the map. A turn reaching TurnDone is
		// over, so anything still live is stale — a missed terminal event, a
		// late Usage/child dispatch after summarizeSubagentResult, or a cancelled
		// turn. Without this, the panel renders "live N running" with ticking
		// durations forever (#25).
		m.ensureSubagentRuns()
		for id := range m.subagents {
			delete(m.subagents, id)
		}
		if e.Err != nil && e.Err.Error() != "" && !strings.Contains(e.Err.Error(), "context canceled") {
			m.commitLine(wrapForViewport(i18n.M.ErrorPrefix+" "+e.Err.Error(), m.width, activeCLITheme.warn))
		}
	}
}

// finalizeStreamed freezes any in-progress reasoning + answer into scrollback so
// a following event line lands after them, preserving chronological order.
func (m *chatTUI) finalizeStreamed() {
	m.collapseToolOutput(m.toolStreamID)
	m.commitReasoning()
	m.commitPending()
}
