package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"roach-code/internal/textarea"

	"roach-code/internal/event"
	"roach-code/internal/i18n"
	"roach-code/internal/provider"
)

// newTestChatTUI builds a chatTUI with just the pieces the streaming/commit and
// completion paths need, for unit tests that don't run the bubbletea loop.
func newTestChatTUI() chatTUI {
	commit := []string{}
	ti := textarea.New()
	ti.SetWidth(80)
	return chatTUI{
		input:                ti,
		width:                80,
		submittedInputCursor: -1,
		nextPasteID:          1,
		reasoningLineIdx:     -1,
		reasoningTextIdx:     -1,
		answerIdx:            -1,
		toolStreamIdx:        -1,
		readRollupIdx:        -1,
		reasoning:            &strings.Builder{},
		pending:              &strings.Builder{},
		pendingCommit:        &commit,
		renderer:             newMarkdownRenderer(80),
	}
}

// TestIngestSeparatesReasoningFromAnswer proves the thinking marker plus its live
// text appear as reasoning streams, collapse to a "thought for Ns" summary (the
// streamed text removed) when the answer begins, and the answer commits as its
// own distinct entry.
func TestIngestSeparatesReasoningFromAnswer(t *testing.T) {
	m := newTestChatTUI()

	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "…reasoning…"}) // thinking → marker + live text
	if len(m.transcript) != 2 || !strings.Contains(m.transcript[0], "thinking") {
		t.Fatalf("thinking marker should appear at once, transcript=%v", m.transcript)
	}
	if !strings.Contains(m.transcript[1], "…reasoning…") {
		t.Fatalf("reasoning text should stream live below the marker, transcript=%v", m.transcript)
	}

	m.ingestEvent(event.Event{Kind: event.Text, Text: "Hello answer"}) // answer begins → block collapses
	// Collapse is non-destructive: the marker stays and the text slot is blanked
	// (the full text is retained in m.thoughts for Ctrl+O / approval reveal), so the
	// marker + an empty slot remain rather than the entry being spliced away.
	if len(m.transcript) != 2 || !strings.Contains(m.transcript[0], "thought for") {
		t.Fatalf("block should collapse to a duration summary, transcript=%v", m.transcript)
	}
	if strings.Contains(strings.Join(m.transcript, "\n"), "…reasoning…") {
		t.Fatalf("collapsed reasoning text should be hidden, transcript=%v", m.transcript)
	}
	if m.pending.String() != "Hello answer" {
		t.Errorf("answer should be live in pending, got %q", m.pending.String())
	}
	if m.reasoning.Len() != 0 {
		t.Errorf("reasoning buffer should be cleared after commit")
	}

	m.commitPending() // turn end
	if len(m.transcript) != 3 || !strings.Contains(m.transcript[2], "Hello") {
		t.Fatalf("answer should commit as a separate entry, transcript=%v", m.transcript)
	}
}

// TestVerboseReasoningInsertsTextUnderSummary proves /verbose mode keeps the full
// thinking text, placed beneath the collapsed duration summary.
func TestVerboseReasoningInsertsTextUnderSummary(t *testing.T) {
	m := newTestChatTUI()
	m.showReasoning = true

	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "step one "})
	m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "step two"})
	m.ingestEvent(event.Event{Kind: event.Text, Text: "Answer"}) // closes the block

	if len(m.transcript) != 2 {
		t.Fatalf("verbose block should be summary + text, transcript=%v", m.transcript)
	}
	if !strings.Contains(m.transcript[0], "thought for") {
		t.Errorf("first line should be the duration summary, got %q", m.transcript[0])
	}
	if !strings.Contains(m.transcript[1], "step one") || !strings.Contains(m.transcript[1], "step two") {
		t.Errorf("verbose text should appear under the summary, got %q", m.transcript[1])
	}
}

// TestIngestEventFlushesAnswer confirms an event line (e.g. a tool dispatch)
// finalizes the answer streamed before it, preserving order in scrollback.
func TestIngestEventFlushesAnswer(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.Text, Text: "partial answer "})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{Name: "read_file", Args: `{"path":"x"}`}})
	// answer, then a blank spacer, then the tool line.
	if n := len(*m.pendingCommit); n != 3 {
		t.Fatalf("answer + spacer + event line should be three commits, got %d: %v", n, *m.pendingCommit)
	}
	if !strings.Contains((*m.pendingCommit)[0], "partial answer") {
		t.Errorf("first commit should be the buffered answer, got %q", (*m.pendingCommit)[0])
	}
	if strings.TrimSpace((*m.pendingCommit)[1]) != "" {
		t.Errorf("second commit should be a blank spacer, got %q", (*m.pendingCommit)[1])
	}
	if !strings.Contains((*m.pendingCommit)[2], "Read(x)") {
		t.Errorf("third commit should be the tool card, got %q", (*m.pendingCommit)[2])
	}
	if m.pending.Len() != 0 {
		t.Errorf("answer buffer should be drained after the event line")
	}
}

// TestStreamAnswerFlushesCompletedParagraphs proves a multi-paragraph answer
// appears chunk by chunk: a closed paragraph renders to scrollback while the
// still-streaming one stays buffered, and turn end flushes the remainder.
func TestStreamAnswerFlushesCompletedParagraphs(t *testing.T) {
	m := newTestChatTUI()

	m.ingestEvent(event.Event{Kind: event.Text, Text: "First paragraph.\n\nSecond para "})
	if m.answerIdx < 0 {
		t.Fatalf("a completed paragraph should open a streamed answer block")
	}
	joined := strings.Join(m.transcript, "\n")
	if !strings.Contains(joined, "First paragraph.") {
		t.Errorf("completed paragraph should be on screen, transcript=%v", m.transcript)
	}
	if strings.Contains(joined, "Second para") {
		t.Errorf("the still-streaming paragraph must stay buffered, transcript=%v", m.transcript)
	}

	m.ingestEvent(event.Event{Kind: event.Text, Text: "is done now."})
	m.ingestEvent(event.Event{Kind: event.Message})
	final := strings.Join(m.transcript, "\n")
	if !strings.Contains(final, "First paragraph.") || !strings.Contains(final, "Second para is done now.") {
		t.Errorf("turn end should flush the whole answer, transcript=%v", m.transcript)
	}
	if m.pending.Len() != 0 || m.answerIdx != -1 {
		t.Errorf("answer state should reset after commit, pending=%d idx=%d", m.pending.Len(), m.answerIdx)
	}
}

// TestFlushableMarkdownPrefixKeepsOpenFence proves a blank line inside an unclosed
// fenced code block is not a flush boundary — the half-written block stays buffered
// so it never renders mangled, while prose before the fence does flush.
func TestFlushableMarkdownPrefixKeepsOpenFence(t *testing.T) {
	open := "intro line\n\n```go\nfunc f() {\n\n\t// still typing"
	if got := flushableMarkdownPrefix(open); got != "intro line" {
		t.Errorf("open fence: flushable prefix = %q, want %q", got, "intro line")
	}

	closed := "```go\ncode\n\nmore\n```\n\ntrailing"
	if got := flushableMarkdownPrefix(closed); got != "```go\ncode\n\nmore\n```" {
		t.Errorf("closed fence: flushable prefix = %q", got)
	}

	if got := flushableMarkdownPrefix("no boundary yet"); got != "" {
		t.Errorf("no blank line should flush nothing, got %q", got)
	}
}

func TestApprovalBannerClampsToWidth(t *testing.T) {
	i18n.DetectLanguage("en")
	m := newTestChatTUI()
	m.width = 80
	m.pendingApproval = &event.Approval{Tool: "bash", Subject: strings.Repeat("x", 200)}
	plain := ansi.Strip(m.renderApprovalBanner())
	for _, want := range []string{"1. Allow once", "2. Allow this session", "3. Deny"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("tool approval banner lost choice %q:\n%s", want, plain)
		}
	}
	// The dangerous tail of a long bash command must survive truncation (middle-clip),
	// and the gate must state the sandbox confinement so approval isn't blind.
	if !strings.Contains(plain, "…") {
		t.Errorf("long bash subject should be middle-clipped (head … tail):\n%s", plain)
	}
	if !strings.Contains(plain, "UNCONFINED") {
		t.Errorf("a bash gate with unknown sandbox must fail safe and warn UNCONFINED:\n%s", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if w := ansi.StringWidth(line); w > m.width {
			t.Fatalf("tool approval banner width = %d, want <= %d:\n%s", w, m.width, line)
		}
	}

	// An enforcing sandbox is stated as such (not the red unconfined warning).
	m.bashSandbox = bashSandboxStatus{state: "enforce"}
	if enforce := ansi.Strip(m.renderApprovalBanner()); !strings.Contains(enforce, "sandbox: enforce") {
		t.Errorf("an enforce sandbox must be stated on the bash gate:\n%s", enforce)
	}
}

func TestRenderTUIBannerClampsNarrowWidth(t *testing.T) {
	i18n.DetectLanguage("en")
	width := 32
	out := ansi.Strip(renderTUIBanner(strings.Repeat("model-", 20), "", "", width))
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Fatalf("startup banner width = %d, want <= %d:\n%s", w, width, line)
		}
	}
}

// live under its card via the cyber rail connector, then collapses to a line-count
// summary when the result lands.
func TestToolProgressStreamsThenCollapses(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "b1", Name: "bash", Args: `{"command":"go test ./..."}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "b1", Output: "ok pkg/a\n"}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "b1", Output: "ok pkg/b\n"}})

	joined := strings.Join(m.transcript, "\n")
	if !strings.Contains(joined, "ok pkg/a") || !strings.Contains(joined, "ok pkg/b") {
		t.Fatalf("live output should be visible while running:\n%s", joined)
	}
	if !strings.Contains(joined, "│") {
		t.Fatalf("live output should hang off the │ rail connector:\n%s", joined)
	}

	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "b1", Name: "bash", Output: "ok pkg/a\nok pkg/b\n"}})
	joined = strings.Join(m.transcript, "\n")
	if strings.Contains(joined, "ok pkg/a") {
		t.Fatalf("output should collapse after completion:\n%s", joined)
	}
	if !strings.Contains(joined, "2 lines") {
		t.Fatalf("collapsed block should summarize the line count:\n%s", joined)
	}
}

// TestToolWorkingLineThenClears proves a dispatched tool that streams no output
// (e.g. codegraph_context) shows a live "working · Ns" line so it doesn't look
// frozen, and that the line clears on the result instead of collapsing to
// "0 lines".
func TestToolWorkingLineThenClears(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "c1", Name: "codegraph_context", Args: `{"q":"x"}`}})

	m.tickToolRunning() // one elapsed tick fills the placeholder
	joined := strings.Join(m.transcript, "\n")
	if !strings.Contains(joined, "│") || !strings.Contains(joined, "working") {
		t.Fatalf("a running tool should show a 'working' progress line:\n%s", joined)
	}

	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "c1", Name: "codegraph_context"}})
	joined = strings.Join(m.transcript, "\n")
	if strings.Contains(joined, "working") {
		t.Fatalf("working line should clear after the result:\n%s", joined)
	}
	if strings.Contains(joined, "0 lines") {
		t.Fatalf("a no-output tool must not collapse to '0 lines':\n%s", joined)
	}
	if m.toolStreamIdx != -1 {
		t.Fatalf("tool block should be closed after the result, idx=%d", m.toolStreamIdx)
	}
}

// TestToolProgressTailCap proves the live block only keeps the last
// toolStreamTailLines lines so a chatty build doesn't flood scrollback.
func TestToolProgressTailCap(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "b1", Name: "bash", Args: `{"command":"x"}`}})
	for i := 0; i < toolStreamTailLines+5; i++ {
		m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "b1", Output: "line" + string(rune('A'+i)) + "\n"}})
	}
	block := m.transcript[m.toolStreamIdx]
	if got := strings.Count(block, "\n") + 1; got > toolStreamTailLines {
		t.Fatalf("live block kept %d lines, want <= %d:\n%s", got, toolStreamTailLines, block)
	}
	if strings.Contains(block, "lineA") {
		t.Fatalf("oldest line should have scrolled out of the tail:\n%s", block)
	}
}

func TestSubagentChildToolsNestUnderTaskAndSummarize(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "task1", Name: "task", Args: `{"description":"inspect ui","prompt":"x"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "task1/r1", ParentID: "task1", Name: "read_file", Args: `{"path":"a.go"}`}})

	panel := ansi.Strip(m.renderSubagentPanel())
	if !strings.Contains(panel, "AGENTS") || !strings.Contains(panel, "Read a.go") {
		t.Fatalf("live subagent panel should show current child activity:\n%s", panel)
	}

	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "task1/r1", ParentID: "task1", Name: "read_file", Output: "ok"}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "task1", Name: "task", Output: "done"}})

	plain := ansi.Strip(strings.Join(m.transcript, "\n"))
	if !strings.Contains(plain, "Task(inspect ui)") {
		t.Fatalf("task summary should keep the task label:\n%s", plain)
	}
	if !strings.Contains(plain, "│ ● Read(a.go)") {
		t.Fatalf("subagent child tool should be nested under the task rail:\n%s", plain)
	}
	if !strings.Contains(plain, "1 tools") {
		t.Fatalf("task summary should include child tool count:\n%s", plain)
	}
	if panel := ansi.Strip(m.renderSubagentPanel()); panel != "" {
		t.Fatalf("completed foreground subagent should leave the live panel:\n%s", panel)
	}
}

func TestSubagentPanelOverflowKeepsOpenRail(t *testing.T) {
	m := newTestChatTUI()
	for i := 0; i < 13; i++ {
		m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
			ID:   fmt.Sprintf("task%d", i),
			Name: "task",
			Args: fmt.Sprintf(`{"description":"scan %02d","prompt":"x"}`, i),
		}})
	}

	panel := ansi.Strip(m.renderSubagentPanel())
	if !strings.Contains(panel, "SUBAGENTS live 13 running") {
		t.Fatalf("subagent panel should show the live header:\n%s", panel)
	}
	if !strings.Contains(panel, "  ├─ ● Task · scan 05") || !strings.Contains(panel, "  ╰─ +4 more · /jobs") {
		t.Fatalf("overflow should keep the last shown subagent on an open rail before the footer:\n%s", panel)
	}
	assertPanelLinesFit(t, panel, m.width)
}

func TestSubagentChildProgressUsesNestedRail(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "task1", Name: "task", Args: `{"description":"run tests","prompt":"x"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "task1/b1", ParentID: "task1", Name: "bash", Args: `{"command":"go test"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolProgress, Tool: event.Tool{ID: "task1/b1", Output: "ok pkg\n"}})

	plain := ansi.Strip(strings.Join(m.transcript, "\n"))
	if !strings.Contains(plain, "│ ● Bash(go test)") || !strings.Contains(plain, "│   │ ok pkg") {
		t.Fatalf("nested tool progress should stay under the subagent rail:\n%s", plain)
	}
}

func TestBackgroundSubagentSummaryIsNotMarkedDone(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "task1", Name: "task", Args: `{"description":"long scan","prompt":"x","run_in_background":true}`}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "task1", Name: "task", Output: "Started background task"}})

	plain := ansi.Strip(strings.Join(m.transcript, "\n"))
	if !strings.Contains(plain, "background") {
		t.Fatalf("background task should be marked background, not done:\n%s", plain)
	}
}

func TestFailedSubagentSummaryIncludesReason(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "task1", Name: "task", Args: `{"description":"scan auth","prompt":"x"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "task1", Name: "task", Err: "permission denied"}})

	plain := ansi.Strip(strings.Join(m.transcript, "\n"))
	if !strings.Contains(plain, "failed") || !strings.Contains(plain, "permission denied") {
		t.Fatalf("failed subagent summary should include status and reason:\n%s", plain)
	}
	if panel := ansi.Strip(m.renderSubagentPanel()); panel != "" {
		t.Fatalf("failed foreground subagent should leave the live panel:\n%s", panel)
	}
}

func TestCancelledSubagentSummaryUsesCancelledStatus(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "task1", Name: "task", Args: `{"description":"scan auth","prompt":"x"}`}})
	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "task1", Name: "task", Err: "context canceled"}})

	plain := ansi.Strip(strings.Join(m.transcript, "\n"))
	if !strings.Contains(plain, "cancelled") {
		t.Fatalf("cancelled subagent summary should use cancelled status:\n%s", plain)
	}
	if strings.Contains(plain, "failed") {
		t.Fatalf("cancelled subagent summary should not be marked failed:\n%s", plain)
	}
}

func TestSubagentSummarySanitizesControlCodes(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
		ID:   "task1",
		Name: "task",
		Args: `{"description":"scan \u001b[31mred\u001b[0m","prompt":"x"}`,
	}})
	panel := m.renderSubagentPanel()
	if strings.Contains(panel, "\x1b[31m") || strings.Contains(panel, "\x1b[0m") {
		t.Fatalf("live subagent panel should not expose raw ANSI controls:\n%q", panel)
	}

	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{
		ID:   "task1",
		Name: "task",
		Err:  "bad \x1b[2Jreason",
	}})
	joined := strings.Join(m.transcript, "\n")
	if strings.Contains(joined, "\x1b[31m") || strings.Contains(joined, "\x1b[2J") {
		t.Fatalf("terminal subagent summary should not expose raw ANSI controls:\n%q", joined)
	}
	plain := ansi.Strip(joined)
	if !strings.Contains(plain, "scan red") || !strings.Contains(plain, "bad reason") {
		t.Fatalf("sanitized text should preserve readable content:\n%s", plain)
	}
}

func TestSubagentUsageTracksTokensWithoutParentTurnLeak(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "task1", Name: "task", Args: `{"description":"token scan","prompt":"x"}`}})
	m.ingestEvent(event.Event{Kind: event.Usage, ParentID: "task1", Usage: &provider.Usage{TotalTokens: 2400, CompletionTokens: 400}})

	panel := ansi.Strip(m.renderSubagentPanel())
	if !strings.Contains(panel, "2K tokens") {
		t.Fatalf("live subagent panel should show subagent token count:\n%s", panel)
	}
	if m.turnTokens != 0 {
		t.Fatalf("subagent usage must not increment parent turn token counter, got %d", m.turnTokens)
	}

	m.ingestEvent(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "task1", Name: "task", Output: "done"}})
	plain := ansi.Strip(strings.Join(m.transcript, "\n"))
	if !strings.Contains(plain, "2K tokens") {
		t.Fatalf("terminal subagent summary should show token count:\n%s", plain)
	}
}

func TestSubagentApprovalShowsRequester(t *testing.T) {
	m := newTestChatTUI()
	m.ingestEvent(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "task1", Name: "task", Args: `{"description":"review db","prompt":"x"}`}})
	m.ingestEvent(event.Event{Kind: event.ApprovalRequest, ParentID: "task1", Approval: event.Approval{ID: "a1", Tool: "bash", Subject: "go test"}})

	banner := ansi.Strip(m.renderApprovalBanner())
	if !strings.Contains(banner, "requested by review db") {
		t.Fatalf("approval banner should show the requesting subagent:\n%s", banner)
	}
	panel := ansi.Strip(m.renderSubagentPanel())
	if !strings.Contains(panel, "Awaiting approval Bash go test") {
		t.Fatalf("live subagent panel should show approval activity:\n%s", panel)
	}
}

// TestReasoningViewBounded proves the live thinking view stays bounded under a
// long stream — the fix for the O(n²)/multi-GB re-render of the full thought.
func TestReasoningViewBounded(t *testing.T) {
	m := newTestChatTUI()
	for i := 0; i < 5000; i++ {
		m.ingestEvent(event.Event{Kind: event.Reasoning, Text: "some thinking text token "})
	}
	if len(m.reasoningView) > reasoningViewMax {
		t.Fatalf("reasoningView unbounded: %d > %d", len(m.reasoningView), reasoningViewMax)
	}
	if c := strings.Count(m.transcript[m.reasoningTextIdx], "\n") + 1; c > reasoningTailLines {
		t.Fatalf("live reasoning block kept %d lines, want <= %d", c, reasoningTailLines)
	}
}
