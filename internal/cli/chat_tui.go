package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rivo/uniseg"
	"roach-code/internal/textarea"

	"roach-code/internal/command"
	"roach-code/internal/control"
	"roach-code/internal/event"
	"roach-code/internal/i18n"
	"roach-code/internal/memory"
	"roach-code/internal/plugin"
	"roach-code/internal/provider"
	"roach-code/internal/skill"
)

// chatTUI is a bubbletea Model that runs a chat session in the terminal's
// normal buffer (no alt-screen). Finalized output — user bubbles, tool dispatch
// lines, usage lines, reasoning, and the rendered assistant answer — is
// committed to the native scrollback via tea.Println, so the wheel, scrollbar,
// and copy all work like any CLI. The bubbletea-managed region is only the
// bottom — input box, status line, an optional approval banner, and the
// autocomplete menu — and it is kept a stable height (it changes only on
// discrete user actions, never per streamed token) so the renderer commits
// scrollback cleanly without stranding the input box's border lines. This
// mirrors the Ink <Static> pattern to freeze finished output into
// scrollback while re-rendering just the active prompt.
type chatTUI struct {
	ctrl         *control.Controller
	label        string
	missing      string // missing-key warning surfaced once in the banner, "" when ready
	version      string // build version for update checks
	updateNotice string // non-empty when a newer release exists

	width  int
	height int

	input   textarea.Model
	spinner spinner.Model
	// inputHadWide records whether the previous keystroke left a wide (CJK) glyph
	// in the composer, so the Warp full-repaint workaround also covers the frame
	// that deletes the last wide glyph (see forceInputRepaint).
	inputHadWide bool

	submittedInputs      []string
	submittedInputCursor int
	submittedInputDraft  string
	pastedBlocks         []pastedBlock
	nextPasteID          int

	state    tuiState
	runStart time.Time
	elapsed  int
	// turnTokens accumulates this turn's output tokens (summed from per-step Usage
	// events) for the live "↓N" readout in the running status line.
	turnTokens int

	// balance is the last-fetched wallet-balance readout (e.g. "¥110.00"), "" when
	// the provider declares no balance_url or a fetch failed. Refreshed async on
	// startup and after each turn so the status line stays roughly current without
	// blocking the event loop.
	balance string

	// todoArgs is the latest todo_write call's raw args; it drives the task list
	// pinned just above the input (see renderTodoPanel). "" when there's no list.
	// Persists across turns until the work completes or a new session starts.
	todoArgs string

	// history is a resumed session's messages, committed to scrollback once on
	// the first WindowSizeMsg so a reopened chat shows its prior transcript.
	history []provider.Message

	// reasoning accumulates the in-progress thinking stream (dim); pending
	// accumulates the in-progress answer (raw markdown). They are committed to
	// scrollback (reasoning collapsed by default, answer markdown-rendered) when they
	// finalize — at a tool/usage boundary or turn end — not previewed live, so
	// the bottom region stays a stable height. pendingCommit queues finalized
	// lines so a single Update emits exactly one ordered tea.Println.
	reasoning     *strings.Builder
	pending       *strings.Builder
	pendingCommit *[]string
	renderer      *mdRenderer
	showReasoning bool // Ctrl+O / /verbose: show raw thinking text in the CLI
	// reasoningLineIdx is the transcript index of the live "▎ thinking…" marker
	// while a reasoning block streams; it's rewritten to "▎ thought for Ns" when
	// the block closes. -1 when no block is open. transcriptDirty forces a
	// viewport re-feed after that in-place rewrite (length is unchanged).
	reasoningLineIdx int
	// reasoningTextIdx is the transcript index of the live reasoning text block
	// (the block right after the marker), streamed in as the model thinks. On
	// collapse the text is RETAINED (in m.thoughts) and merely hidden, not deleted,
	// so /verbose can expand it retroactively and an approval can reveal it. -1 when none.
	reasoningTextIdx int
	// reasoningView is a bounded trailing window (≤ reasoningViewMax bytes) of the
	// streaming thought, rendered live; the full text stays in reasoning for verbose.
	reasoningView []byte
	thinkStart    time.Time
	// shimmerPhase advances once per spinner tick to animate the live thinking
	// indicator's ▓▒░ sheen (ambient decoration only; see shimmer()).
	shimmerPhase int
	// ultragoalActive is true while a turn engaged via the `ultragoal` keyword
	// runs, so View() glows the dynamic-workflow sparkle banner. Cleared on
	// TurnDone (see chat_turn.go).
	ultragoalActive bool
	// ultragoalPhase advances the idle preview shimmer (the banner shown while the
	// composer holds the keyword, before Enter); ultragoalTicking guards its tick
	// loop so only one runs at a time. The running banner uses shimmerPhase instead.
	ultragoalPhase   int
	ultragoalTicking bool
	// thoughts records every committed reasoning block (marker + text transcript
	// indices plus the full raw text) so collapse is non-destructive: Ctrl+O toggles
	// them all in place, and a pending approval force-expands the most recent one.
	thoughts []committedThought
	// approvalExpandedThought is the index into thoughts force-expanded while an
	// approval is pending (so its rationale stays on screen during the decision); -1
	// when none — restored to the verbose setting once the approval resolves.
	approvalExpandedThought int
	// answerIdx is the transcript index of the streaming answer block (rewritten in
	// place as completed paragraphs arrive); -1 when none is open. answerFlushed is
	// how many bytes of pending have already been rendered into it, so a Text packet
	// that doesn't close a new paragraph re-renders nothing.
	answerIdx     int
	answerFlushed int
	// toolStreamIdx is the transcript index of a running tool's live-output block
	// (streamed via ToolProgress under the tool card); -1 when none. toolStreamID
	// is the call ID it belongs to. Only a bounded tail is kept — the last few
	// complete lines (toolTail) plus the in-progress one (toolPartial) — so a
	// high-output command can't balloon memory or cost O(n²) re-splitting;
	// toolLineCount feeds the collapse summary.
	toolStreamIdx int
	toolStreamID  string
	// toolStreamPrefix is prepended to the output rail for nested subagent tools,
	// so child tool output stays visually attached to the subagent row.
	toolStreamPrefix string
	toolTail         []string
	toolPartial      string
	toolLineCount    int
	// readRollupIdx points at the visible read_file card for a consecutive run of
	// Read calls. Subsequent read_file dispatches rewrite that one line with the
	// latest path instead of stacking multiple nearly-identical Read cards.
	// -1 when the current visible sequence is not a read_file run.
	readRollupIdx int
	// readRollupParent scopes the read_file rollup. Top-level reads and each
	// subagent parent keep independent visible rows instead of overwriting one
	// another.
	readRollupParent string
	// subagents tracks visible task/run_skill subagent invocations by parent tool
	// call ID. Qwen keeps an always-on live roster plus a terminal scrollback
	// summary; roach-code's TUI mirrors that in the transcript by grouping child
	// tool calls under this parent and replacing the parent line with a terminal
	// summary when the run finishes.
	subagents map[string]*subagentRun
	// toolStreamStart / toolStreamFrame drive the "╰─ working · Ns" line shown
	// under a dispatched tool that hasn't produced output yet, so a slow tool
	// (e.g. codegraph_context) reads as making progress rather than frozen.
	toolStreamStart time.Time
	toolStreamFrame int
	transcriptDirty bool
	eventCh         chan event.Event
	started         bool // banner + resumed history committed once
	// Banner glow: while a FRESH session sits idle on the welcome screen, the ROACH
	// wordmark breathes a slow shimmer sweep (the static-art analogue of the thinking
	// verb's shimmer). bannerIdx is its transcript entry (-1 = none/frozen), bannerPhase
	// drives the sweep, bannerAnimate gates the idle ticker — switched off the instant
	// the first turn starts, and never on for a resumed session.
	// bannerLive: while a fresh/empty welcome screen is up, the ROACH wordmark is
	// rendered LIVE in View() (recomputed from bannerPhase every frame) instead of
	// sitting as a static transcript entry — the same direct-render path as the
	// thinking line, so terminals like Warp (which don't repaint in-place viewport
	// changes) animate it smoothly at the frame rate with no forced full redraws.
	// Set on a fresh start, cleared (and the banner committed static) on the first turn.
	bannerLive  bool
	bannerPhase int

	// transcript holds every finalized line commitLine emits; the viewport
	// renders a scrollable window of it (alt-screen owns the grid, so there's no
	// native terminal scrollback). sel is the live left-drag text selection.
	transcript   []string
	wrappedLines []string // transcript wrapped to viewport width (rendered each frame)
	viewport     viewport.Model
	sel          selection
	// autoScroll drives edge-drag scrolling: -1 up, +1 down, 0 off. dragX is the
	// column the drag is held at, so the ticker can extend the selection head.
	autoScroll int
	dragX      int

	// The user bubble is echoed to scrollback immediately on Enter (bubbleStartIdx
	// marks where in the transcript it landed). It stays "un-sendable" until the
	// first response packet arrives: pressing Esc/Ctrl+C before then pops those
	// lines back off the transcript and restores the text to the input box, leaving
	// no trace. bubblePending is true from startTurn until the first packet confirms
	// the send or it's un-sent; turnDiscarded then swallows the turn's
	// already-buffered events until its TurnDone settles.
	pendingRestore string
	pendingPastes  []string
	bubbleStartIdx int
	bubblePending  bool
	turnDiscarded  bool

	// pendingApproval holds the tool-call approval currently shown in the banner
	// (nil when none). While set, the controller's run goroutine is blocked
	// awaiting ctrl.Approve and key input is captured to answer it.
	pendingApproval *event.Approval
	// pendingApprovalParent is the subagent parent tool call that requested the
	// current approval, when the gate came from inside a subagent.
	pendingApprovalParent string
	// approvalCursor is the highlighted choice row (0 allow once · 1 allow session ·
	// 2 deny) while pendingApproval is set. It defaults to Deny for destructive
	// calls so a reflexive Enter denies them; see handleApprovalKey.
	approvalCursor int
	// bashSandbox summarises the session's bash confinement (set at startup from
	// config + sandbox.Available()) so a bash gate can state, at the decision moment,
	// whether the command runs confined.
	bashSandbox bashSandboxStatus

	// chooser holds the `ask` tool's question card (nil when none). While set, the
	// run goroutine is blocked awaiting ctrl.AnswerQuestion and keys drive the card.
	chooser *chooser

	// rewind holds the Esc-Esc / "/rewind" picker (nil when closed); while set,
	// keys drive it and it renders as an overlay. lastEsc times the double-Esc
	// gesture that opens it on an empty composer.
	rewind *rewindPicker
	// resumePick is the interactive "/resume" session picker overlay. Non-nil
	// while the user browses saved sessions with ↑/↓ and confirms with Enter.
	resumePick *resumePicker
	// resumeArgPvKey/resumeArgPvText cache the transcript preview shown beneath
	// the "/resume <n>" argument completion, so navigating that menu doesn't
	// re-read the highlighted session from disk on every keystroke. The key is
	// "path|width"; both clear when the completion isn't /resume's argument.
	resumeArgPvKey  string
	resumeArgPvText string
	// jobsPick is the interactive "/jobs" background-task roster. It mirrors
	// Qwen's task dialog within roach-code's existing bottom-panel UI.
	jobsPick *jobsPicker
	lastEsc  time.Time

	// lastCtrlCAt records when Ctrl+C was pressed while idle on an empty
	// composer, enabling a "press again to quit" confirmation pattern (1.5s
	// window). Reset when Ctrl+C clears non-empty input instead.
	lastCtrlCAt time.Time

	// host is the running MCP servers (nil when no plugins). The TUI reads
	// prompts (slash commands), resources (@-references), and server status
	// (/mcp) from it.
	host *plugin.Host

	// commands are custom slash commands loaded from .roach-code/commands; each renders
	// its template with the typed args and sends the result as a turn.
	commands []command.Command

	// skills are the discoverable skills (built-in + user/project); each is offered
	// in the slash menu as "/<name>" and managed via /skill.
	skills []skill.Skill

	// buildController builds a fresh controller on a model ref, carrying prior
	// history across (set by chatREPL; it must NOT touch this model — the swap
	// happens in runModelSubcommand on the running copy). nil disables /model.
	// modelRef is the active "provider/model" ref, marked current in the picker.
	buildController func(ref string, carry []provider.Message) (*control.Controller, error)
	modelRef        string
	effortLevel     string // "" when the current provider/model has no configurable effort

	// outputStyle is the active output-style name (config agent.output_style),
	// shown as the current entry in the /output-style listing. "" = default.
	outputStyle string

	// statuslineCmd is the user's custom status-line command (config
	// [statusline].command); "" disables it. statuslineOut caches its latest
	// one-line stdout, refreshed at startup and after each turn and rendered in
	// place of the built-in data row.
	statuslineCmd string
	statuslineOut string

	// modelSwitchPending is true while an async /model build is in flight.
	modelSwitchPending bool
	// pendingModelSwitch holds the tea.Cmd that triggers the async build.
	pendingModelSwitch tea.Cmd
	// oldControllers accumulates controllers retired by /model switches.
	// They cannot be closed during the switch (Close runs SessionEnd hooks
	// and kills plugin subprocesses, both of which corrupt the terminal's
	// raw mode). Instead they are closed at process exit when the terminal
	// is already being restored.
	oldControllers []*control.Controller

	// completion is the live autocomplete menu (slash commands; @-refs later).
	completion completion
}

type tuiState int

const (
	tuiIdle tuiState = iota
	tuiRunning
)

// newChatTUI assembles the initial model. The controller has already been wired
// with an event sink that feeds eventCh; the TUI issues commands to it and
// renders the events it emits. Label, history, host, and commands are read from
// the controller, so a resumed session pre-populates scrollback.
func newChatTUI(ctrl *control.Controller, missing string, eventCh chan event.Event, termW int, version string) chatTUI {
	ti := textarea.New()
	ti.Prompt = ""
	ti.CharLimit = 16384
	ti.SetHeight(1)
	ti.ShowLineNumbers = false
	// DynamicHeight: textarea auto-grows to fit visual (soft-wrapped) lines,
	// capped by MaxHeight so long pastes don't crowd the chat scrollback.
	ti.DynamicHeight = true
	ti.MaxHeight = maxInputRows
	applyTextareaTheme(&ti)
	// Use the real terminal cursor (not a styled virtual one) so View can place
	// it at the insertion point and IME candidate windows anchor to the input.
	ti.SetVirtualCursor(false)
	// Plain Enter submits (the chatTUI handler intercepts it), so the textarea's
	// own InsertNewline binding moves to Alt+Enter / Ctrl+J / Shift+Enter.
	ti.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j", "shift+enter"))
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = themeStyle(activeCLITheme.accent)

	commitBuf := []string{}
	return chatTUI{
		ctrl:                    ctrl,
		label:                   ctrl.Label(),
		missing:                 missing,
		version:                 version,
		input:                   ti,
		spinner:                 sp,
		submittedInputCursor:    -1,
		nextPasteID:             1,
		reasoningLineIdx:        -1,
		reasoningTextIdx:        -1,
		answerIdx:               -1,
		toolStreamIdx:           -1,
		readRollupIdx:           -1,
		approvalExpandedThought: -1,
		reasoning:               &strings.Builder{},
		pending:                 &strings.Builder{},
		pendingCommit:           &commitBuf,
		renderer:                newMarkdownRenderer(termW),
		eventCh:                 eventCh,
		history:                 ctrl.History(),
		host:                    ctrl.Host(),
		commands:                ctrl.Commands(),
		skills:                  ctrl.Skills(),
		viewport:                viewport.New(viewport.WithWidth(termW)),
	}
}

// prompts returns the MCP prompts discovered at startup (nil when no plugins).
func (m *chatTUI) prompts() []plugin.Prompt {
	if m.host == nil {
		return nil
	}
	return m.host.Prompts()
}

func (m chatTUI) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		waitForAgentEvent(m.eventCh),
		fetchBalance(m.ctrl),
		m.runStatusline(),         // nil (no-op) unless a custom status line is configured
		bannerFrameTick(),         // welcome-banner glow frames (~60fps, only while bannerLive)
		checkUpdateCmd(m.version), // async update check; result surfaces in the banner
	)
}

func (m chatTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	wasAtBottom := m.viewport.AtBottom()
	prevLines := len(m.transcript)
	prevWidth := m.width
	prevYOff := m.viewport.YOffset()

	next, cmd := m.update(msg)
	cm := next.(chatTUI)

	contentW := cm.width - 1 // last column is the scrollbar
	if contentW < 1 {
		contentW = 1
	}
	cm.viewport.SetWidth(contentW)
	cm.viewport.SetHeight(cm.transcriptHeight())
	// Re-feed only when the content grew or the width changed (re-wrapping is
	// the expensive part); a bare scroll or spinner tick keeps the offset.
	if len(cm.transcript) != prevLines || cm.width != prevWidth || cm.transcriptDirty {
		wrapped := wrapTranscript(strings.Join(cm.transcript, "\n"), contentW)
		cm.viewport.SetContent(wrapped)
		cm.wrappedLines = strings.Split(wrapped, "\n")
		if wasAtBottom {
			cm.viewport.GotoBottom() // tail-follow: stay pinned to newest output
		}
	}
	cm.transcriptDirty = false
	// Any viewport scroll (wheel, PgUp/PgDn, edge auto-scroll, or tail-follow to
	// newest output) shifts the whole window. Some terminals (Warp) mishandle
	// the renderer's scroll/insert-line optimization and strand stale rows, so
	// force a full clear+redraw whenever the offset actually moved.
	if cm.viewport.YOffset() != prevYOff {
		return cm, tea.Batch(tea.ClearScreen, cmd)
	}
	return cm, cmd
}

// update runs the model's message handling. Update wraps it to keep the
// transcript viewport sized, fed, and tail-following after every message.
func (m chatTUI) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	inputDebugLog(msg) // no-op unless ROACH_INPUT_DEBUG is set

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(msg.Width - 4)
		m.renderer = newMarkdownRenderer(msg.Width)
		// Commit the banner — and a resumed session's transcript — once, now
		// that the width is known.
		if !m.started {
			m.started = true
			// Resolve any resumed transcript first (it may be empty — e.g. a session
			// that only holds the system prompt, which is exactly what `roach` with no
			// args resumes — so len(history)>0 does NOT mean there's anything to show).
			var histSecs []string
			if len(m.history) > 0 {
				r := newMarkdownRenderer(msg.Width)
				histSecs = replaySectionsFor(m.history, msg.Width, r)
				m.history = nil
			}
			// The banner is ALWAYS its own transcript entry so the glow can re-render
			// just it. Arm the welcome sweep whenever the banner is the only thing on
			// screen — fresh OR a resumed-but-empty session (no visible transcript). With
			// real conversation below it the banner scrolls off, so keep it static there.
			// Live glow when the banner is the only thing on screen (fresh, or a
			// resumed-but-empty session) and the hero art fits: render it LIVE in View()
			// instead of committing it, so it animates smoothly at the frame rate. With
			// real conversation, or too narrow, commit the static banner as usual.
			if msg.Width >= roachArtWidth()+3 && len(histSecs) == 0 {
				m.bannerLive = true
			} else {
				m.commitLine(strings.TrimRight(renderTUIBanner(m.label, m.missing, m.updateNotice, msg.Width), "\n"))
				for _, sec := range histSecs {
					m.commitLine(strings.TrimRight(sec, "\n"))
				}
			}
		}

	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.viewport.ScrollUp(3)
		case tea.MouseWheelDown:
			m.viewport.ScrollDown(3)
		}
		return m, nil

	case tea.MouseClickMsg:
		// Right-click copies the active selection (Windows Terminal convention);
		// left-press in the transcript region begins a text selection.
		if msg.Button == tea.MouseRight && m.sel.active && !m.sel.empty() {
			text := m.selectedText()
			m.sel = selection{}
			return m, tea.Batch(copyToClipboard(text), finalize(m, cmds))
		}
		if msg.Button == tea.MouseLeft && msg.Y < m.viewport.Height() {
			at := m.transcriptCaret(msg.X, msg.Y)
			m.sel = selection{active: true, anchor: at, head: at}
			m.autoScroll = 0
		}
		return m, nil

	case tea.MouseMotionMsg:
		// Drag extends the live selection (CellMotion only reports motion while
		// a button is held, so this is a drag). A drag held against the top or
		// bottom edge starts an auto-scroll ticker so the selection can run past
		// the visible window.
		if m.sel.active {
			m.sel.head = m.transcriptCaret(msg.X, msg.Y)
			m.dragX = msg.X
			prev := m.autoScroll
			m.autoScroll = edgeScrollDir(msg.Y, m.viewport.Height())
			if m.autoScroll != 0 && prev == 0 {
				return m, autoScrollTick()
			}
		}
		return m, nil

	case autoScrollMsg:
		// One edge-scroll step: scroll a single line, drag the selection head to
		// the edge row, and keep ticking until the drag ends, leaves the edge, or
		// the viewport can't scroll further (so it can't run away to the end).
		if !m.sel.active || m.autoScroll == 0 {
			return m, nil
		}
		edgeY := 0
		if m.autoScroll > 0 {
			m.viewport.ScrollDown(1)
			edgeY = m.viewport.Height() - 1
		} else {
			m.viewport.ScrollUp(1)
		}
		m.sel.head = m.transcriptCaret(m.dragX, edgeY)
		// Stop at the boundary so a held edge can't run away to the very end.
		if (m.autoScroll > 0 && m.viewport.AtBottom()) || (m.autoScroll < 0 && m.viewport.AtTop()) {
			m.autoScroll = 0
			return m, nil
		}
		return m, autoScrollTick()

	case tea.MouseReleaseMsg:
		// Release finalizes the selection. A drag that actually selected something
		// auto-copies it to the clipboard (the Claude Code convention — "just drag
		// to copy"), keeping the highlight on as the "what was copied" cue; a
		// right-click still re-copies. A plain click (no drag) clears any prior
		// selection.
		m.autoScroll = 0 // stop edge auto-scroll
		// An active selection can only have come from a left-press + drag, so the
		// release ends that drag regardless of which button the terminal reports on
		// the release event (some report MouseNone / a different button in SGR mode,
		// which would otherwise drop the copy). A non-empty drag copies to the
		// clipboard and then drops the highlight — the drag is finished, so the
		// selection clears on release rather than lingering. A plain click clears too.
		if m.sel.active {
			text := m.selectedText()
			m.sel = selection{}
			if text != "" {
				return m, tea.Batch(copyToClipboard(text), finalize(m, cmds))
			}
		}
		return m, nil

	case tea.PasteMsg:
		if m.state != tuiRunning && m.attachPastedImages(msg.Content) {
			return m, finalize(m, cmds)
		}
		if ref, ok := pastedFileRef(msg.Content); ok {
			m.input.InsertString(ref + " ")
			m.growInputToFit()
			m.updateCompletion()
			return m, finalize(m, cmds)
		}
		// An idle paste that carries no text is the tell-tale of an image (or other
		// non-text payload) the terminal can't deliver through the PTY: some
		// terminals (Warp) route Ctrl+V through bracketed paste rather than a key
		// event, so the ctrl+v handler never fires. Probe the OS clipboard for an
		// image directly so image paste works regardless of how Ctrl+V is routed.
		if m.state != tuiRunning && strings.TrimSpace(msg.Content) == "" {
			cmds = append(cmds, pasteClipboardImage())
			return m, finalize(m, cmds)
		}
		if !m.chooserTyping() && m.pendingApproval == nil && m.rewind == nil && m.shouldFoldPaste(msg.Content) {
			m.insertFoldedPaste(msg.Content)
			m.growInputToFit()
			m.updateCompletion()
			return m, finalize(m, cmds)
		}

	case tea.KeyPressMsg:
		// Any keystroke dismisses a finished selection (copy is a right-click).
		m.sel = selection{}
		// Transcript scroll keys work in any state (PgUp/PgDn are never text).
		switch msg.String() {
		case "pgup":
			m.viewport.PageUp()
			return m, finalize(m, cmds)
		case "pgdown":
			m.viewport.PageDown()
			return m, finalize(m, cmds)
		}
		// A question card is modal: keys drive it. In its free-text ("Type
		// something") mode, the keystroke goes to the textarea — Enter confirms the
		// custom answer, Esc backs out of typing — so input/IME work as usual.
		if m.chooser != nil {
			if m.chooser.typing {
				switch msg.String() {
				case "enter":
					val := strings.TrimSpace(m.input.Value())
					m.input.Reset()
					m.input.SetHeight(1)
					m.chooser.typing = false
					if val == "" {
						return m, finalize(m, cmds)
					}
					m.chooser.custom[m.chooser.tab] = val
					m.chooser.sel[m.chooser.tab] = map[int]bool{}
					return m.chooserAdvance()
				case "esc":
					m.chooser.typing = false
					m.input.Reset()
					m.input.SetHeight(1)
					return m, finalize(m, cmds)
				}
				var ic tea.Cmd
				m.input, ic = m.input.Update(msg)
				cmds = append(cmds, ic)
				m.growInputToFit()
				return m, finalize(m, cmds)
			}
			return m.handleChooserKey(msg)
		}
		// The rewind picker is modal while open: keys navigate it.
		if m.rewind != nil {
			return m.handleRewindKey(msg)
		}
		// The resume picker is modal while open: keys navigate it.
		if m.resumePick != nil {
			return m.handleResumePickerKey(msg)
		}
		// The jobs picker is modal while open: keys navigate, inspect, or kill jobs.
		if m.jobsPick != nil {
			return m.handleJobsPickerKey(msg)
		}
		// A pending tool approval is modal: keystrokes answer it (y/a/n, Enter,
		// Esc) rather than reaching the input.
		if m.pendingApproval != nil {
			return m.handleApprovalKey(msg)
		}
		// While the autocomplete menu is open it captures navigation/accept keys
		// (↑/↓ move, Tab/Enter accept, Esc close); everything else falls through
		// to the textarea and re-filters the menu at the end of Update.
		if m.completion.active {
			switch msg.String() {
			case "up":
				m.moveCompletion(-1)
				return m, nil
			case "down":
				m.moveCompletion(1)
				return m, nil
			case "tab", "enter":
				// Enter on a highlighted "/resume <n>" session is terminal — choosing
				// the session IS the action — so accept the index AND submit in one
				// press. Before, Enter only filled the number (and with "1" a prefix of
				// "10" the menu even re-captured it), so it looked like nothing happened.
				// Tab still just inserts, for anyone who wants to edit before running.
				if msg.String() == "enter" && m.isResumeArgCompletion() && m.completion.sel < len(m.completion.items) {
					m.acceptCompletion()
					m.completion = completion{} // accept may have re-filtered; force-close
					m.resumeArgPvKey, m.resumeArgPvText = "", ""
					break // fall through to regular Enter → runSlashCommand("/resume N")
				}
				// When Enter is pressed and the completion has exactly one item
				// already fully present in the input, close the menu and let Enter
				// fall through to submit the command (/resume 3 → resume session 3).
				if msg.String() == "enter" && len(m.completion.items) == 1 {
					tok := m.input.Value()[m.completion.replaceFrom:]
					if tok == m.completion.items[0].insert {
						m.completion = completion{}
						break // fall through to regular Enter
					}
				}
				m.acceptCompletion()
				return m, nil
			case "esc":
				m.completion = completion{}
				if m.state == tuiRunning {
					break // a turn is running — also cancel it via the main Esc handler
				}
				return m, nil
			}
		}
		switch msg.String() {
		case "up":
			if m.state != tuiRunning && m.recallSubmittedInput(-1) {
				return m, nil
			}
		case "down":
			if m.state != tuiRunning && m.recallSubmittedInput(1) {
				return m, nil
			}
			if m.state != tuiRunning && m.ctrl != nil && strings.TrimSpace(m.input.Value()) == "" && len(m.ctrl.AllJobs()) > 0 {
				m.openJobsPicker()
				return m, nil
			}
		default:
			m.resetSubmittedInputRecall()
		}
		switch msg.String() {
		case "esc":
			// "Back out" of the most specific in-progress state: un-send a just-sent
			// turn (server not yet replied), cancel a streaming turn, turn YOLO off,
			// or clear typed-but-unsent input. Scrollback is the terminal's now,
			// so there's no viewport to dismiss.
			switch {
			case m.state == tuiRunning && m.bubblePending:
				m.unsendPending()
			case m.state == tuiRunning:
				m.ctrl.Cancel()
			case m.ctrl.Bypass():
				m.ctrl.SetBypass(false) // back out of YOLO
			default:
				// Idle with nothing to back out: a double-Esc on an empty composer
				// opens the rewind picker; a first Esc just
				// arms it. Non-empty input clears as before.
				if strings.TrimSpace(m.input.Value()) == "" {
					if !m.lastEsc.IsZero() && time.Since(m.lastEsc) < 600*time.Millisecond {
						m.lastEsc = time.Time{}
						m.openRewind()
					} else {
						m.lastEsc = time.Now()
					}
				} else {
					m.input.Reset()
					m.pastedBlocks = nil
				}
			}
			return m, nil
		case "ctrl+c", "super+c", "meta+c":
			if m.state == tuiRunning {
				if m.bubblePending {
					m.unsendPending() // server not yet replied — restore text, leave no trace
				} else {
					m.ctrl.Cancel()
				}
				return m, nil
			}
			// Idle: if the composer has text, a single press clears it (like Esc).
			// On an empty composer, require double-press within 1.5s to quit.
			if strings.TrimSpace(m.input.Value()) != "" {
				m.input.Reset()
				m.pastedBlocks = nil
				m.lastCtrlCAt = time.Time{}
				return m, nil
			}
			if !m.lastCtrlCAt.IsZero() && time.Since(m.lastCtrlCAt) < 1500*time.Millisecond {
				return m, tea.Quit
			}
			m.lastCtrlCAt = time.Now()
			m.notice(i18n.M.CtrlCQuitHint)
			return m, finalize(m, nil)
		case "ctrl+d":
			return m, tea.Quit
		case "ctrl+z":
			return m, tea.Suspend
		case "ctrl+v", "ctrl+shift+v", "super+v", "meta+v":
			if m.state == tuiRunning {
				return m, nil
			}
			cmds = append(cmds, pasteClipboard())
			return m, finalize(m, cmds)
		case "ctrl+y":
			if m.state == tuiRunning {
				return m, nil
			}
			cmds = append(cmds, pasteClipboardImage())
			return m, finalize(m, cmds)
		case "ctrl+o":
			m.toggleVerboseReasoning(m.state != tuiRunning)
			return m, finalize(m, cmds)
		case "shift+tab":
			// Toggle YOLO; allowed mid-turn so the user can flip the gate while a run
			// is in flight.
			m.cycleMode()
			return m, nil
		case "enter":
			line := strings.TrimSpace(m.input.Value())
			if m.state == tuiRunning {
				// Goal management must stay reachable while a turn is running: `/goal <cond>`
				// is explicitly allowed to arm/update the standing goal for the in-flight
				// turn tail, and `/goal clear` must be able to stop a goal loop.
				if line == "/goal" || strings.HasPrefix(line, "/goal ") {
					m.rememberSubmittedInput(line)
					m.input.Reset()
					m.input.SetHeight(1)
					m.pastedBlocks = nil
					cmds = append(cmds, m.runSlashCommand(line))
					return m, finalize(m, cmds)
				}
				return m, nil // ignore Enter while a turn is in flight
			}
			if m.modelSwitchPending {
				return m, nil // ignore Enter while /model switch is building
			}

			if line == "" {
				return m, nil
			}
			if line == "exit" || line == "quit" || line == ":q" {
				return m, tea.Quit
			}
			m.rememberSubmittedInput(line)

			// "#<note>" quick-adds a memory line locally, no model turn —
			// mirroring the "#" memory shortcut.
			if strings.HasPrefix(line, "#") {
				m.input.Reset()
				m.input.SetHeight(1)
				m.pastedBlocks = nil
				note := strings.TrimSpace(strings.TrimPrefix(line, "#"))
				if note == "" {
					m.notice(i18n.M.QuickRememberEmpty)
				} else if path, err := m.ctrl.QuickAdd(memory.ScopeProject, note); err != nil {
					m.notice("memory: " + err.Error())
				} else {
					m.notice(fmt.Sprintf(i18n.M.QuickRememberDoneFmt, path))
				}
				return m, finalize(m, cmds)
			}

			// "ultragoal <goal>" engages dynamic-workflow mode: a sparkle banner
			// glows while the turn is steered to accomplish <goal> via run_workflow.
			if task, ok := ultragoalTask(line); ok {
				m.input.Reset()
				m.input.SetHeight(1)
				m.pastedBlocks = nil
				if task == "" {
					m.notice("ultragoal: type a goal — e.g. ultragoal review every .go file in internal/workflow")
					return m, finalize(m, cmds)
				}
				cmds = append(cmds, m.startUltragoal(task))
				return m, finalize(m, cmds)
			}

			// Slash commands run locally without going through the model. A
			// '/'-leading line that's actually a dragged file path is an attachment,
			// not a command, so it's rewritten to an @reference instead.
			if strings.HasPrefix(line, "/") {
				if ref, ok := control.FileRefLine(line); ok {
					line = ref
				} else {
					m.input.Reset()
					m.input.SetHeight(1)
					m.pastedBlocks = nil
					cmds = append(cmds, m.runSlashCommand(line))
					return m, finalize(m, cmds)
				}
			}

			sentLine := m.expandPastedBlocks(line)
			m.input.Reset()
			m.input.SetHeight(1)

			// @references (local files / MCP resources, including inline image
			// attachments) are resolved off the event loop by the controller; the turn
			// starts when they resolve (refsResolvedMsg).
			if m.ctrl.HasRefs(sentLine) {
				cmds = append(cmds, m.resolveRefs(sentLine, sentLine, line))
				return m, finalize(m, cmds)
			}

			cmds = append(cmds, m.startTurnWithRaw(sentLine, sentLine, line, line))
			return m, finalize(m, cmds)
		}

	case agentEventMsg:
		e := event.Event(msg)
		m.ingestEvent(e)
		turnDone := e.Kind == event.TurnDone
		// Coalesce a burst: the goroutine that produced this event has already
		// exited (a Cmd reads the channel once), so it's safe to drain the events
		// already buffered and ingest them now. One re-wrap then covers the whole
		// batch instead of one per event — bounds the O(transcript) re-render cost
		// when bash output or reasoning floods in. Capped so a sustained flood
		// still yields to render periodically.
	drain:
		for drained := 0; drained < maxEventDrain; drained++ {
			select {
			case e2 := <-m.eventCh:
				m.ingestEvent(e2)
				if e2.Kind == event.TurnDone {
					turnDone = true
				}
			default:
				break drain
			}
		}
		cmds = append(cmds, waitForAgentEvent(m.eventCh))
		// A turn just spent tokens (and money) — refresh the balance readout and
		// the custom status line (its context/cost inputs just changed).
		if turnDone {
			cmds = append(cmds, fetchBalance(m.ctrl))
			if c := m.runStatusline(); c != nil {
				cmds = append(cmds, c)
			}
		}

	case balanceMsg:
		m.balance = msg.text

	case statuslineMsg:
		m.statuslineOut = msg.out

	case compactDoneMsg:
		if msg.err != nil {
			m.notice(fmt.Sprintf("%s: %v", i18n.M.SlashCompactFailed, msg.err))
		} else {
			_ = m.ctrl.Snapshot()
		}

	case modelSwitchMsg:
		m.modelSwitchPending = false
		m.pendingModelSwitch = nil
		if msg.err != nil {
			m.notice("model: " + msg.err.Error())
			// Build failed — no old controller to retire.
		} else {
			m.ctrl = msg.ctrl
			m.label = msg.label
			m.commands = msg.commands
			m.skills = msg.skills
			m.host = msg.host
			m.modelRef = msg.ref
			m.refreshEffortStatus()
			// Stash the old controller for cleanup at exit. It cannot be
			// closed here or in the build goroutine — Close() runs
			// SessionEnd hooks and kills plugin subprocesses, both of
			// which corrupt bubbletea's terminal raw mode.
			if msg.oldCtrl != nil {
				m.oldControllers = append(m.oldControllers, msg.oldCtrl)
			}
			m.notice(fmt.Sprintf(i18n.M.ModelSwitchedFmt, m.label))
			cmds = append(cmds, fetchBalance(m.ctrl))
			// Do NOT re-issue waitForAgentEvent here — the goroutine from the
			// last agentEventMsg handler is still blocked on the same channel.
			// Starting a second one creates a race: two goroutines compete on
			// p.Send (unbuffered), and the receiver may read them out of order,
			// garbling the streamed text (words appear reordered).
		}

	case promptResolvedMsg:
		switch {
		case msg.err != nil:
			m.commitLine(wrapForViewport(i18n.M.ErrorPrefix+" "+msg.err.Error(), m.width, activeCLITheme.warn))
		case strings.TrimSpace(msg.sent) == "":
			m.notice(i18n.M.SlashPromptEmpty)
		default:
			cmds = append(cmds, m.startTurn(msg.sent, msg.display, msg.display))
		}

	case refsResolvedMsg:
		for _, e := range msg.errs {
			m.notice(e) // surface a fetch failure but still send the turn
		}
		cmds = append(cmds, m.startMessageTurnWithRaw(msg.msg, msg.display, msg.restore, msg.restore))

	case clipboardImageMsg:
		if msg.err != nil {
			m.notice("paste image: " + msg.err.Error())
			break
		}
		m.insertImageRef(msg.path)

	case clipboardPasteMsg:
		switch {
		case msg.err != nil:
			m.notice("paste: " + msg.err.Error())
		case msg.path != "":
			m.insertImageRef(msg.path)
		case msg.text != "":
			if m.attachPastedImages(msg.text) {
				return m, finalize(m, cmds)
			}
			if ref, ok := pastedFileRef(msg.text); ok {
				m.input.InsertString(ref + " ")
			} else if m.shouldFoldPaste(msg.text) {
				m.insertFoldedPaste(msg.text)
			} else {
				m.input.InsertString(msg.text)
			}
			m.growInputToFit()
			m.updateCompletion()
			return m, finalize(m, cmds)
		}

	case elapsedTickMsg:
		if m.state == tuiRunning {
			m.elapsed = int(time.Since(m.runStart).Seconds())
			cmds = append(cmds, elapsedTick())
		}

	case ultragoalFrameMsg:
		// Idle preview shimmer: advance the phase and repaint while the composer
		// still holds the keyword; stop the loop the instant it no longer does.
		// ultragoalTicking is the single source of truth for "a loop is alive", so
		// the kick below never double-schedules a second frame chain.
		if m.ultragoalPreviewing() {
			m.ultragoalPhase++
			m.ultragoalTicking = true
			cmds = append(cmds, ultragoalFrameTick())
		} else {
			m.ultragoalTicking = false
		}

	case bannerFrameMsg:
		// Welcome-banner glow frame. The banner is rendered LIVE in View() from
		// bannerPhase (a direct-render element, like the thinking line), so advancing
		// the phase + letting View() repaint is all it takes — no transcript mutation,
		// no forced ClearScreen. Reschedules only while live, so it runs at the frame
		// rate ONLY on the welcome screen and stops the instant the banner freezes.
		if m.bannerLive {
			m.bannerPhase++
			cmds = append(cmds, bannerFrameTick())
		}

	case updateCheckMsg:
		if msg.latest != "" && versionNewer(m.version, msg.latest) {
			m.updateNotice = fmt.Sprintf(i18n.M.UpdateAvailableFmt, msg.latest)
		}

	case spinner.TickMsg:
		if m.state == tuiRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
			m.shimmerPhase++    // advance the live thinking-indicator sheen
			m.tickToolRunning() // rotate the working braille at the spinner's fps
		}
	}

	var ic tea.Cmd
	m.input, ic = m.input.Update(msg)
	cmds = append(cmds, ic)
	m.growInputToFit()
	// Re-filter the autocomplete menu against the freshly-edited input.
	if _, ok := msg.(tea.KeyPressMsg); ok {
		m.updateCompletion()
		// Warp workaround: its renderer corrupts in-place updates over CJK wide
		// glyphs (gaps / ghosts), but draws a fresh full frame correctly. When the
		// composer holds — or just held — a wide rune, force a full repaint so the
		// edited line is redrawn from scratch instead of incrementally diffed.
		wide := hasWideRune(m.input.Value())
		if forceInputRepaint(wide, m.inputHadWide) {
			cmds = append(cmds, tea.ClearScreen)
		}
		m.inputHadWide = wide
	}

	// Light the idle ultragoal preview shimmer the moment the composer starts
	// holding the keyword: kick its tick loop once (guarded so only one runs).
	if m.ultragoalPreviewing() && !m.ultragoalTicking {
		m.ultragoalTicking = true
		cmds = append(cmds, ultragoalFrameTick())
	}

	return m, finalize(m, cmds)
}

// hasWideRune reports whether s contains a rune wider than one cell (CJK and
// other East Asian wide glyphs), i.e. the case Warp mis-renders incrementally.
func hasWideRune(s string) bool {
	return uniseg.StringWidth(s) > len([]rune(s))
}

// forceInputRepaint decides whether to force a full repaint (tea.ClearScreen)
// for the composer this frame. Warp mishandles incremental redraws over wide
// glyphs, so on Warp we repaint whenever the input holds (wideNow) or just held
// (widePrev) one — the latter covers deleting the last wide glyph. Other
// terminals diff fine and are left untouched. ROACH_FULL_REPAINT=1/0 overrides.
func forceInputRepaint(wideNow, widePrev bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ROACH_FULL_REPAINT"))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	if os.Getenv("TERM_PROGRAM") != "WarpTerminal" {
		return false
	}
	return wideNow || widePrev
}

var (
	// Input box: only top + bottom borders, no sides. The concrete colors are
	// refreshed from the active CLI theme during startup.
	inputBoxStyle       lipgloss.Style
	approvalBannerStyle lipgloss.Style
	todoPanelStyle      lipgloss.Style
	statusBlockStyle    lipgloss.Style
	workingStyle        lipgloss.Style
)

func (m chatTUI) View() tea.View {
	boxW := m.width
	if boxW < 10 {
		boxW = 10
	}
	box := inputBoxStyle.Width(boxW).Render(m.input.View())
	// ultragoal glow: the composer's own frame shimmers while the keyword is held
	// (idle preview) or while the engaged turn runs — the trigger lives in the
	// input box itself, not a separate banner, so the status rows below are
	// untouched (their height is unchanged, so the statusline never gets pushed off).
	if m.ultragoalGlowing() {
		box = shimmerInputBox(box, m.ultragoalGlowPhase())
	}

	var modeTag string
	switch {
	case m.ctrl.Bypass():
		if !colorEnabled {
			// NO_COLOR / pipe: the solid red alarm field is gone, so shout in text —
			// skip-all-approvals must never read as a quiet, unemphasised word.
			modeTag = "[!YOLO]"
		} else {
			modeTag = lipgloss.NewStyle().
				Background(lipgloss.Color("#e5484d")).
				Foreground(lipgloss.Color("#ffffff")).
				Bold(true).
				Padding(0, 1).
				Render("YOLO")
		}
	default:
		modeTag = glitchMark("Auto")
	}
	// An armed goal rides a persistent accent chip beside the mode pill — the
	// always-visible "a goal is active" marker (the live line shows the grind).
	if cond, iter := m.ctrl.Goal(); cond != "" {
		chip := "◉ goal"
		if iter > 0 {
			chip += fmt.Sprintf(" ×%d", iter)
		}
		modeTag += " " + themeFg(activeCLITheme.accent, chip)
	}

	ctxTag := m.contextTag()
	var status string
	statusSep := themeFg(activeCLITheme.faint, "│") // structure, not signal — glitchMark is the row's one accent
	switch {
	case m.rewind != nil:
		status = "  " + modeTag + " " + statusSep + " ⟲ rewind"
	case m.resumePick != nil:
		status = "  " + modeTag + " " + statusSep + " " + i18n.M.StatusResumePicker
	case m.chooser != nil:
		status = "  " + modeTag + " " + statusSep + " " + i18n.M.ChatStatusQuestion
	case m.pendingApproval != nil:
		status = "  " + modeTag + " " + statusSep + " " + i18n.M.ChatStatusToolApproval
	case m.ctrl.Bypass():
		status = "  " + modeTag + " " + statusSep + " " + i18n.M.ChatStatusYoloIdle
	default:
		status = "  " + modeTag + " " + statusSep + " " + i18n.M.ChatStatusIdle
	}
	// The spinning "thinking…" indicator is its own line ABOVE the input box (shown
	// only while a turn runs); the status/data rows stay below. This mirrors Claude
	// Code: live progress over the composer, shortcuts + stats under it.
	var working string
	if m.state == tuiRunning {
		// A breathing star, the verb shimmering left→right, then dim metadata —
		// the live element is the WORD, not a row of blocks.
		meta := fmt.Sprintf(i18n.M.ChatStatusThinkingFmt, m.elapsed)
		// The star is the lamp's single visible heartbeat: its SHAPE steps at 5fps
		// (thinkGlyph) while its LIGHT breathes continuously on the 2s lamp period,
		// floored at 0.55 so it never reads as "off".
		star := themeFg(mixColor(activeCLITheme.faint, activeCLITheme.accent, 0.55+0.45*lampBreath(m.shimmerPhase+10)), thinkGlyph(m.shimmerPhase))
		// While a goal loop runs, the live verb becomes "pursuing goal ×N" and trails
		// the model's own next step, so the user watches WHAT it's chasing — and, with
		// no iteration cap, this is the indicator they decide to Esc against.
		verb := i18n.M.ChatThinking
		tail := ""
		if cond, iter := m.ctrl.Goal(); cond != "" {
			verb = i18n.M.GoalPursuing
			if iter > 0 {
				verb += fmt.Sprintf(" ×%d", iter)
			}
			if nudge := m.ctrl.GoalNudge(); nudge != "" {
				tail = dim(" — " + clampPlain(nudge, 44))
			}
		}
		working = "  " + star + " " + shimmer(verb, m.shimmerPhase) + tail + " " + dim(meta)
		if m.turnTokens > 0 {
			tps := ""
			if m.elapsed > 0 {
				if t := m.turnTokens / m.elapsed; t > 0 {
					tps = " @" + shortTPS(t) + "/s"
				}
			}
			working += dim(" · ↓" + shortTokens(m.turnTokens) + tps)
		}
	}
	// Second status row: the live data (model, effort, context gauge, cache rates,
	// jobs, balance). It lives on its own fixed row so it's always shown in full
	// rather than being truncated off the end of the status line. Two rows is a
	// fixed height, so unlike a wrap-when-long status it doesn't reintroduce
	// resize ghosting.
	var data []string
	if mt := m.modelTag(); mt != "" {
		data = append(data, mt)
	}
	if et := m.effortTag(); et != "" {
		data = append(data, et)
	}
	if ctxTag != "" {
		data = append(data, ctxTag)
	}
	if cache := m.cacheTag(); cache != "" {
		data = append(data, cache)
	}
	if jt := m.jobsTag(); jt != "" {
		data = append(data, jt)
	}
	if m.balance != "" {
		data = append(data, themeFg(activeCLITheme.faint, m.balance))
	}
	if ct := m.costTag(); ct != "" {
		data = append(data, ct)
	}
	dataLine := "  "
	dataSep := themeFg(activeCLITheme.faint, " · ") // was surfaceSeam (#241a13, ~invisible) — now a legible quiet divider
	if len(data) > 0 {
		dataLine = "  " + themeFg(activeCLITheme.accent, "╾") + " " + strings.Join(data, dataSep)
	}
	// A configured custom status line replaces the built-in data row entirely.
	if m.statuslineCmd != "" && m.statuslineOut != "" {
		dataLine = "  " + m.statuslineOut
	}

	// Bottom region pinned under the transcript viewport: optional panels, the
	// input box, then the two status rows. Its height feeds transcriptHeight so
	// the viewport above fills exactly the rest of the screen.
	var parts []string
	rowsAboveBox := 0 // terminal rows occupied by todo/banner/menu before the input box
	// Each pinned panel appends itself and adds its rows via appendPanel; the row
	// formula lives once in panelRowCount so this layout and bottomRows can't drift.
	appendPanel(&parts, &rowsAboveBox, m.renderTodoPanel())
	appendPanel(&parts, &rowsAboveBox, m.renderSubagentPanel())
	appendPanel(&parts, &rowsAboveBox, m.renderApprovalBanner())
	appendPanel(&parts, &rowsAboveBox, m.renderChooser())
	appendPanel(&parts, &rowsAboveBox, m.renderRewind())
	appendPanel(&parts, &rowsAboveBox, m.renderResumePicker())
	appendPanel(&parts, &rowsAboveBox, m.renderJobsPicker())
	appendPanel(&parts, &rowsAboveBox, m.renderCompletion())
	// Layout: the working spinner (when running) above the box; the input box; then
	// the two status rows (line 1 = mode + shortcuts/state, line 2 = live data).
	// Each row is clamped to width independently so neither wraps; padding to full
	// width keeps a short row from leaving stale cells from the prior frame.
	if working != "" {
		parts = append(parts, workingStyle.Width(boxW).MaxWidth(boxW).Render(clampStatusLine(working, boxW)))
		rowsAboveBox++
	}
	statusBlock := clampStatusLine(status, boxW) + "\n" + clampStatusLine(dataLine, boxW)
	parts = append(parts, box, statusBlockStyle.Width(boxW).MaxWidth(boxW).Render(statusBlock))

	// Full-screen frame: the thread header on top (amp-style), the transcript
	// viewport beneath it, then the pinned bottom region. Alt-screen owns the
	// grid, so resize repaints cleanly — no scrollback reflow, no ghost borders.
	//
	// Amp-like terminal-native surface: leave the terminal background alone and
	// carry the look through foreground colour, spacing, and low-contrast rules.
	// Forcing a black canvas creates visible patches when the user's terminal
	// theme is not pure black.
	// The fresh welcome screen renders the banner LIVE here (not via the viewport)
	// so it animates smoothly on every terminal — including ones that don't
	// repaint an in-place viewport change. Once a turn starts it's committed
	// static and the viewport takes over.
	header := m.renderThreadHeader()
	top := m.renderTranscript()
	if m.bannerLive {
		top = m.renderWelcomeBanner()
	}
	frame := top + "\n" + strings.Join(parts, "\n")
	if header != "" {
		frame = header + "\n" + frame
	}
	v := tea.NewView(frame)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion // wheel scrolls the transcript
	// Anchor the real terminal cursor at the textarea's insertion point so IME
	// candidate windows appear in the input box. input.Cursor() is relative to
	// the textarea; offset by the viewport height + rows above + the box's top
	// border row (+1 column for PaddingLeft).
	if cur := m.input.Cursor(); cur != nil {
		cur.X += 1
		cur.Y += m.headerRows() + m.viewport.Height() + rowsAboveBox + 1
		v.Cursor = cur
	}
	return v
}
