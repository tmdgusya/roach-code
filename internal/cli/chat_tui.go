package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"

	"roach-code/internal/command"
	"roach-code/internal/config"
	"roach-code/internal/control"
	"roach-code/internal/event"
	"roach-code/internal/hook"
	"roach-code/internal/i18n"
	"roach-code/internal/memory"
	"roach-code/internal/outputstyle"
	"roach-code/internal/plugin"
	"roach-code/internal/provider"
	"roach-code/internal/sandbox"
	"roach-code/internal/skill"
	"roach-code/internal/tool"
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
	ctrl    *control.Controller
	label   string
	missing string // missing-key warning surfaced once in the banner, "" when ready

	width  int
	height int

	input   textarea.Model
	spinner spinner.Model

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
	toolTail      []string
	toolPartial   string
	toolLineCount int
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
	lastEsc    time.Time

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

// agentEventMsg is one typed event from the agent's run loop.
type agentEventMsg event.Event

// maxEventDrain caps how many buffered events one Update coalesces before
// yielding to render, so a sustained output flood still shows live progress.
const maxEventDrain = 512

// compactDoneMsg reports that an async /compact pass returned. The card was
// already drawn from the CompactionDone event; this only surfaces a failure and
// snapshots on success.
type compactDoneMsg struct{ err error }

// elapsedTickMsg fires once a second while a turn runs, driving the "thinking
// Ns" counter in the status line.
type elapsedTickMsg struct{}

// balanceMsg carries the result of an async wallet-balance fetch; text is the
// formatted readout ("" when none/failed).
type balanceMsg struct{ text string }

// statuslineMsg carries the latest custom status-line output (one line, ""
// when none/failed).
type statuslineMsg struct{ out string }

// runStatusline runs the user's custom status-line command off the event loop,
// feeding it a small JSON context on stdin and returning its first stdout line.
// A no-op (nil) when no command is configured. Tight timeout so a slow script
// can't stall the UI; failures collapse to an empty line rather than an error.
func (m chatTUI) runStatusline() tea.Cmd {
	cmd := m.statuslineCmd
	if cmd == "" {
		return nil
	}
	used, window := m.ctrl.ContextSnapshot()
	payload, _ := json.Marshal(map[string]any{
		"model":         m.label,
		"contextUsed":   used,
		"contextWindow": window,
	})
	return func() tea.Msg { return statuslineMsg{out: runStatuslineCmd(cmd, string(payload))} }
}

// runStatuslineCmd runs a status-line command with the JSON context on stdin and
// returns its first stdout line (status lines are a single row). A tight timeout
// keeps a slow script from stalling the UI; any failure collapses to "".
func runStatuslineCmd(cmd, stdinPayload string) string {
	res := hook.DefaultSpawner(context.Background(), hook.SpawnInput{
		Command: cmd,
		Stdin:   stdinPayload + "\n",
		Timeout: 2 * time.Second,
	})
	out := strings.TrimSpace(res.Stdout)
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	return out
}

// modelSwitchMsg carries the result of an async /model switch. A nil err means
// the new controller is ready in ctrl; label/commands/skills/host mirror the
// fields that runModelSubcommand used to set synchronously. oldCtrl is the
// previous controller that must be closed after the switch — its cleanup
// (SessionEnd hooks, plugin subprocess kill) is deferred to a tea.Cmd so it
// runs after the render completes, avoiding corruption of the terminal's raw
// mode that would occur if Close() were called from the build goroutine.
type modelSwitchMsg struct {
	ref      string
	ctrl     *control.Controller
	oldCtrl  *control.Controller
	label    string
	commands []command.Command
	skills   []skill.Skill
	host     *plugin.Host
	err      error
}

// fetchBalance queries the provider's wallet balance off the event loop. It's a
// no-op readout ("") when the provider declares no balance_url or the fetch
// fails, so the status line stays quiet rather than surfacing an error.
func fetchBalance(ctrl *control.Controller) tea.Cmd {
	return func() tea.Msg {
		b, err := ctrl.Balance(context.Background())
		if err != nil || b == nil {
			return balanceMsg{}
		}
		return balanceMsg{text: b.Display()}
	}
}

// promptResolvedMsg carries the result of fetching an MCP prompt (an async
// prompts/get). display is the command line echoed as the user bubble; sent is
// the rendered prompt text that becomes the model turn.
type promptResolvedMsg struct {
	display string
	sent    string
	err     error
}

// refsResolvedMsg carries the result of resolving the @references in a
// submitted line (async file reads / MCP resources/read).
type refsResolvedMsg struct {
	sent    string
	display string
	restore string
	block   string
	errs    []string
}

type clipboardImageMsg struct {
	path string
	err  error
}

type clipboardPasteMsg struct {
	path string
	text string
	err  error
}

// newChatTUI assembles the initial model. The controller has already been wired
// with an event sink that feeds eventCh; the TUI issues commands to it and
// renders the events it emits. Label, history, host, and commands are read from
// the controller, so a resumed session pre-populates scrollback.
func newChatTUI(ctrl *control.Controller, missing string, eventCh chan event.Event, termW int) chatTUI {
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
		input:                   ti,
		spinner:                 sp,
		submittedInputCursor:    -1,
		nextPasteID:             1,
		reasoningLineIdx:        -1,
		reasoningTextIdx:        -1,
		answerIdx:               -1,
		toolStreamIdx:           -1,
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

func (m *chatTUI) rememberSubmittedInput(input string) {
	if strings.TrimSpace(input) == "" {
		return
	}
	if len(m.submittedInputs) == 0 || m.submittedInputs[len(m.submittedInputs)-1] != input {
		m.submittedInputs = append(m.submittedInputs, input)
	}
	m.submittedInputCursor = -1
	m.submittedInputDraft = ""
}

func (m *chatTUI) recallSubmittedInput(delta int) bool {
	if len(m.submittedInputs) == 0 {
		return false
	}
	cursor := m.submittedInputCursor
	if cursor < 0 {
		if delta > 0 {
			return false
		}
		if m.input.Line() != 0 {
			return false // first-line Up enters history; lower lines navigate the draft
		}
		m.submittedInputDraft = m.input.Value()
		cursor = len(m.submittedInputs) - 1
	} else {
		cursor += delta
	}

	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(m.submittedInputs) {
		m.submittedInputCursor = -1
		m.input.SetValue(m.submittedInputDraft)
		m.growInputToFit()
		return true
	}
	m.submittedInputCursor = cursor
	m.input.SetValue(m.submittedInputs[cursor])
	m.growInputToFit()
	return true
}

func (m *chatTUI) resetSubmittedInputRecall() {
	m.submittedInputCursor = -1
	m.submittedInputDraft = ""
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
		m.runStatusline(), // nil (no-op) unless a custom status line is configured
		bannerFrameTick(), // welcome-banner glow frames (~60fps, only while bannerLive)
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
				m.commitLine(strings.TrimRight(renderTUIBanner(m.label, m.missing, msg.Width), "\n"))
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
			if m.state == tuiRunning {
				return m, nil // ignore Enter while a turn is in flight
			}
			if m.modelSwitchPending {
				return m, nil // ignore Enter while /model switch is building
			}
			line := strings.TrimSpace(m.input.Value())

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
		sent := msg.sent
		if msg.block != "" {
			sent = "Referenced context:\n\n" + msg.block + "\n\n" + msg.sent
		}
		cmds = append(cmds, m.startTurnWithRaw(sent, msg.display, msg.restore, msg.restore))

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
	}

	return m, finalize(m, cmds)
}

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

// renderWelcomeBanner renders the live (animated) ROACH wordmark for the fresh welcome
// screen, filling the transcript area's height so the layout matches the viewport it
// stands in for. It's recomputed from bannerPhase every frame — a small, transient
// string, never accumulated, so a high frame rate adds no memory.
func (m chatTUI) renderWelcomeBanner() string {
	h := m.transcriptHeight()
	if h <= 0 {
		return ""
	}
	banner := strings.TrimRight(renderTUIBannerAt(m.label, m.missing, m.width, m.bannerPhase), "\n")
	lines := strings.Split(banner, "\n")
	rows := make([]string, h)
	for i := range rows {
		if i < len(lines) {
			rows[i] = lines[i]
		}
	}
	return strings.Join(rows, "\n")
}

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

// toolStreamTailLines caps how many trailing output lines a running tool shows;
// the live block scrolls within this window so a chatty build doesn't flood.
const toolStreamTailLines = 8

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
		m.toolTail = m.toolTail[:0]
		m.toolPartial = ""
		m.toolLineCount = 0
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
		m.transcript[m.toolStreamIdx] = surfaceWrap(connectorBlock([]string{dim(fmt.Sprintf("%d lines", n))}), m.width)
	}
	m.transcriptDirty = true
	m.toolStreamIdx = -1
	m.toolStreamID = ""
	m.toolTail = m.toolTail[:0]
	m.toolPartial = ""
	m.toolLineCount = 0
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
	m.toolTail = m.toolTail[:0]
	m.toolPartial = ""
	m.toolLineCount = 0
	m.toolStreamStart = time.Now()
	m.toolStreamFrame = 0
	m.toolStreamIdx = len(m.transcript)
	m.commitLine(surfaceWrap(connectorBlock([]string{dim(fmt.Sprintf(i18n.M.ChatToolWorkingFmt, toolWorkingFrames[0], 0))}), m.width))
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
	m.transcript[m.toolStreamIdx] = surfaceWrap(connectorBlock([]string{dim(fmt.Sprintf(i18n.M.ChatToolWorkingFmt, frame, secs))}), m.width)
	m.transcriptDirty = true
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
		m.transcript[m.answerIdx] = block
		m.transcriptDirty = true
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
		m.transcript[m.answerIdx] = block
		m.transcriptDirty = true
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

// handleApprovalKey resolves a pending approval from a keystroke and re-arms the
// listener. ↑/↓ (or k/j) move the highlighted choice; Enter activates the
// highlighted row — which DEFAULTS to Deny for destructive calls, so a reflexive
// Enter denies rather than grants. 1/y still allows once, 2/a allows for the
// session, 3/n/Esc deny, and Ctrl-C cancels the whole turn.
func (m chatTUI) handleApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	answer := func(allow, session bool) (tea.Model, tea.Cmd) {
		// Restore the thought force-expanded for this gate to the verbose setting now
		// the decision is made (re-collapse unless /verbose is on).
		if m.approvalExpandedThought >= 0 {
			if m.approvalExpandedThought < len(m.thoughts) {
				m.renderThought(m.thoughts[m.approvalExpandedThought], m.showReasoning, 0)
				m.transcriptDirty = true
			}
			m.approvalExpandedThought = -1
		}
		m.ctrl.Approve(m.pendingApproval.ID, allow, session)
		m.pendingApproval = nil
		return m, nil // the next ApprovalRequest / event arrives on eventCh
	}
	switch msg.String() {
	case "ctrl+c":
		m.ctrl.Cancel() // cancels the run; the approver unblocks via ctx.Done()
		return answer(false, false)
	case "up", "k":
		if m.approvalCursor > 0 {
			m.approvalCursor--
		}
		return m, nil
	case "down", "j":
		if m.approvalCursor < 2 {
			m.approvalCursor++
		}
		return m, nil
	case "ctrl+o":
		// The global Ctrl+O is otherwise swallowed while a gate is up; honour it here
		// so the user can expand/collapse the rationale at the moment they most want
		// to read it, keeping the gated thought open regardless of the new setting.
		m.toggleVerboseReasoning(false)
		if m.approvalExpandedThought >= 0 && m.approvalExpandedThought < len(m.thoughts) {
			m.renderThought(m.thoughts[m.approvalExpandedThought], true, reasoningApprovalLines)
			m.transcriptDirty = true
		}
		return m, nil
	case "enter":
		switch {
		case m.approvalCursor == 0:
			return answer(true, false)
		case m.approvalCursor == 1:
			return answer(true, true)
		default:
			return answer(false, false)
		}
	case "esc":
		return answer(false, false)
	}
	switch strings.ToLower(msg.String()) {
	case "y", "1":
		return answer(true, false)
	case "a", "2":
		return answer(true, true)
	case "n", "3":
		return answer(false, false)
	}
	return m, nil // ignore anything else while awaiting a decision
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
			working += dim(" · ↓" + shortTokens(m.turnTokens))
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
	if todo := m.renderTodoPanel(); todo != "" {
		parts = append(parts, todo)
		rowsAboveBox += strings.Count(todo, "\n") + 1
	}
	if banner := m.renderApprovalBanner(); banner != "" {
		parts = append(parts, banner)
		rowsAboveBox += strings.Count(banner, "\n") + 1
	}
	if card := m.renderChooser(); card != "" {
		parts = append(parts, card)
		rowsAboveBox += strings.Count(card, "\n") + 1
	}
	if card := m.renderRewind(); card != "" {
		parts = append(parts, card)
		rowsAboveBox += strings.Count(card, "\n") + 1
	}
	if card := m.renderResumePicker(); card != "" {
		parts = append(parts, card)
		rowsAboveBox += strings.Count(card, "\n") + 1
	}
	if menu := m.renderCompletion(); menu != "" {
		parts = append(parts, menu)
		rowsAboveBox += strings.Count(menu, "\n") + 1
	}
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

	// Full-screen frame: the transcript viewport on top (it pads to exactly its
	// height), the pinned bottom region beneath. Alt-screen owns the grid, so
	// resize repaints cleanly — no scrollback reflow, no ghost borders.
	//
	// Terminal-native background: we deliberately do NOT paint a page background
	// (no v.BackgroundColor) and do NOT fill cell backgrounds. Every cell keeps the
	// user's own terminal background; only the TEXT is coloured. Painting a warm
	// "ink canvas" kept fighting the terminal — cells after an inline SGR reset fell
	// to the terminal default and read as black boxes, and re-asserting the fill
	// then showed as ragged grey blocks behind menus. The flashy identity lives in
	// the foreground (shimmer, gradients, glyphs), so nothing is lost by dropping it.
	// The fresh welcome screen renders the banner LIVE here (not via the viewport) so
	// it animates smoothly on every terminal — including ones that don't repaint an
	// in-place viewport change. Once a turn starts it's committed static and the
	// viewport takes over.
	top := m.renderTranscript()
	if m.bannerLive {
		top = m.renderWelcomeBanner()
	}
	v := tea.NewView(top + "\n" + strings.Join(parts, "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion // wheel scrolls the transcript
	// Anchor the real terminal cursor at the textarea's insertion point so IME
	// candidate windows appear in the input box. input.Cursor() is relative to
	// the textarea; offset by the viewport height + rows above + the box's top
	// border row (+1 column for PaddingLeft).
	if cur := m.input.Cursor(); cur != nil {
		cur.X += 1
		cur.Y += m.viewport.Height() + rowsAboveBox + 1
		v.Cursor = cur
	}
	return v
}

// compactionCardLines renders a finished compaction as a titled card: a header
// with the message count and trigger, then the structured summary under a dim
// gutter so it reads as one block in scrollback. The summary is also the new
// context base, so this card is the user's window into exactly what was kept.
func compactionCardLines(c event.Compaction) []string {
	trigger := c.Trigger
	switch c.Trigger {
	case "auto":
		trigger = i18n.M.CompactionAuto
	case "manual":
		trigger = i18n.M.CompactionManual
	}
	header := fmt.Sprintf("%s · %d %s · %s", i18n.M.CompactionTitle, c.Messages, i18n.M.CompactionUnit, trigger)
	lines := []string{accent("◆ " + header)}
	for _, ln := range strings.Split(strings.TrimRight(c.Summary, "\n"), "\n") {
		lines = append(lines, dim("  │ "+ln))
	}
	if c.Archive != "" {
		lines = append(lines, dim("  │ archived "+c.Archive))
	}
	return lines
}

// contextTag renders the prompt-vs-context-window gauge for the status line,
// framed around the auto-compaction threshold: it shows how much headroom is
// left until the next compaction, and colours by proximity to that point rather
// than the raw window. Falls back to a plain percentage when compaction is disabled.
func (m chatTUI) contextTag() string {
	used, window := m.ctrl.ContextSnapshot()
	if used == 0 || window == 0 {
		return ""
	}
	pct := used * 100 / window
	ratio := m.ctrl.CompactRatio()
	if ratio <= 0 || ratio >= 1 {
		// Compaction disabled: just the raw gauge, coloured on window fill.
		body := fmt.Sprintf("%s / %s ctx (%d%%)", shortTokens(used), shortTokens(window), pct)
		switch {
		case pct >= 85:
			return themeStyle(activeCLITheme.danger).Render(body)
		case pct >= 60:
			return themeStyle(activeCLITheme.warn).Render(body)
		default:
			return themeFg(activeCLITheme.faint, body)
		}
	}
	threshold := int(ratio * 100)
	// Headroom to the compaction point, as a percentage of the window (clamped at 0).
	left := threshold - pct
	if left < 0 {
		left = 0
	}
	body := fmt.Sprintf("%s ctx (%d%%) · %d%% to compact", shortTokens(used), pct, left)
	switch {
	case pct >= threshold:
		return themeStyle(activeCLITheme.danger).Render(fmt.Sprintf("%s ctx (%d%%) · compacting soon", shortTokens(used), pct))
	case left <= 10:
		return themeStyle(activeCLITheme.warn).Render(body)
	default:
		return themeFg(activeCLITheme.faint, body)
	}
}

// cacheTag renders both prompt cache-hit rates for the status line —
// "cache 88% · avg 78%": the single-turn rate (latest turn, the higher/steeper
// number on a non-compacting DeepSeek session) and the session-aggregate rate
// Σhit/Σ(hit+miss) (the steadier, cost-oriented number that matches the legacy
// dashboard). "" before any cache tokens have been reported.
func (m chatTUI) cacheTag() string {
	// The per-turn "cache N%" is the ONE accent on the data row — the cache-first
	// loop's live heartbeat. avg/loop sit faint beside it as quiet context. Segments
	// are pre-coloured and joined with a faint dot, with NO outer faint wrap (that
	// would smother the inner accent).
	parts := make([]string, 0, 3)
	if u := m.ctrl.LastUsage(); u != nil {
		d := u.CacheHitTokens + u.CacheMissTokens
		if d == 0 {
			d = u.PromptTokens
		}
		if d > 0 {
			parts = append(parts, themeFg(activeCLITheme.faint, "cache ")+
				themeFg(activeCLITheme.accent, fmt.Sprintf("%d%%", u.CacheHitTokens*100/d)))
		}
	}
	if hit, miss := m.ctrl.SessionCache(); hit+miss > 0 {
		parts = append(parts, themeFg(activeCLITheme.faint, fmt.Sprintf("avg %d%%", hit*100/(hit+miss))))
	}
	// While a goal is armed, append its loop-isolated cache rate (Σ-delta since the
	// goal was set) so the cache-first loop's own hit-rate is visible, not diluted by
	// the whole-session average.
	if pct, active := m.ctrl.GoalLoopHitPct(); active && pct > 0 {
		parts = append(parts, themeFg(activeCLITheme.faint, fmt.Sprintf("loop %d%%", pct)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, themeFg(activeCLITheme.faint, " · "))
}

// costTag renders the session-cumulative spend estimate for the status line —
// e.g. "$0.0123" — summed across every turn's token usage priced at the active
// model's rates, so the user sees a live running total of what the session has
// cost. "" when the model has no pricing configured or nothing's been spent yet.
func (m chatTUI) costTag() string {
	cost, symbol := m.ctrl.SessionCost()
	if symbol == "" || cost <= 0 {
		return ""
	}
	return themeFg(activeCLITheme.faint, symbol+formatCost(cost))
}

// formatCost renders a spend figure with precision scaled to its magnitude:
// sub-unit totals (the common case for a short session) keep four decimals so
// fractions of a cent stay visible; larger totals round to two.
func formatCost(c float64) string {
	if c < 1 {
		return fmt.Sprintf("%.4f", c)
	}
	return fmt.Sprintf("%.2f", c)
}

// jobsTag shows the count of running background jobs in the status line. Job
// start/finish emit Notices that arrive on eventCh and re-render the frame, so
// the count stays current without a dedicated tick.
func (m chatTUI) jobsTag() string {
	n := len(m.ctrl.Jobs())
	if n == 0 {
		return ""
	}
	// Plain "N jobs" — no glyph (⚙ has an emoji-presentation variant; technical
	// glyphs like ⌁ aren't reliably in the terminal font). Calm and bulletproof.
	return themeFg(activeCLITheme.faint, fmt.Sprintf("%d jobs", n))
}

func (m chatTUI) modelTag() string {
	if strings.TrimSpace(m.label) == "" {
		return ""
	}
	return bold(themeFg(activeCLITheme.muted, m.label)) // anchors the left edge of the data row
}

func (m chatTUI) effortTag() string {
	if m.effortLevel == "" {
		return ""
	}
	body := "effort " + m.effortLevel
	if m.effortLevel != "auto" {
		return themeFg(activeCLITheme.accent, body)
	}
	return themeFg(activeCLITheme.faint, body)
}

// shortTokens prints token counts compactly: 142_000 → "142K", 1_000_000 → "1M".
func shortTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// renderApprovalBanner is the decision card shown above the input while a tool
// call (or a plan) awaits the user. It shares the chooser's calm chassis and
// navigable ❯-rows: the gate states WHAT will run (action · subject · source, and
// for bash the sandbox confinement), then offers Allow once / Allow this session /
// Deny as highlightable rows. Risk is carried by the border colour and the
// highlighted default (Deny for destructive calls) — not by a permanently-loud
// frame — so the cyber identity lives on everywhere else while the gate itself
// reads calm and honest.
func (m chatTUI) renderApprovalBanner() string {
	w := m.width
	if w < 10 {
		w = 10
	}
	if m.pendingApproval == nil {
		return ""
	}
	toolName := m.pendingApproval.Tool
	name, detail := approvalToolDetails(toolName)
	destructive := approvalDestructive(toolName)

	var lines []string
	// Action line — the first thing the eye hits, no decorative chrome. A single
	// amber ⚠ marks destructive calls; benign reads get a calm copper ▸.
	action := fmt.Sprintf(i18n.M.ToolApprovalActionFmt, name)
	if destructive {
		lines = append(lines, yellow("⚠ ")+bold(action))
	} else {
		lines = append(lines, accent("▸ ")+action)
	}
	// Subject (the command or path) — the one fact about WHAT runs. Width-aware, and
	// for bash middle-clipped so the dangerous tail (&&, |, ; rm) stays visible.
	if subj := strings.TrimSpace(m.pendingApproval.Subject); subj != "" {
		avail := w - 6
		if avail < 12 {
			avail = 12
		}
		if toolName == "bash" {
			lines = append(lines, "  "+cyan(clampMiddle(subj, avail)))
		} else {
			lines = append(lines, "  "+cyan(clampPlain(subj, avail)))
		}
	}
	// Source / intent detail (dim).
	for _, d := range strings.Split(detail, "\n") {
		if d != "" {
			lines = append(lines, "  "+dim(d))
		}
	}
	// Sandbox confinement — bash only, text-token-first so it survives NO_COLOR.
	if toolName == "bash" {
		if sl := m.sandboxStatusLine(); sl != "" {
			lines = append(lines, "  "+sl)
		}
	}
	// Navigable choice rows; the cursor highlights the (risk-aware) default.
	lines = append(lines,
		"",
		rowLine(m.approvalCursor == 0, 1, "", i18n.M.ToolApprovalAllowOnce, false),
		rowLine(m.approvalCursor == 1, 2, "", i18n.M.ToolApprovalAllowSession, false),
		rowLine(m.approvalCursor == 2, 3, "", i18n.M.ToolApprovalDeny, false),
	)
	for i := range lines {
		lines[i] = m.clampBannerLine(lines[i])
	}
	return m.frameApproval(lines, destructive, w)
}

// frameApproval wraps the gate lines in the shared thin chassis, colouring only
// the border by risk (amber for destructive, copper for benign) — respecting
// NO_COLOR via withThemeBorderFG.
func (m chatTUI) frameApproval(lines []string, destructive bool, w int) string {
	c := activeCLITheme.accent
	if destructive {
		c = activeCLITheme.warn
	}
	return withThemeBorderFG(approvalBannerStyle, c).Width(w).Render(strings.Join(lines, "\n"))
}

// approvalDestructive reports whether a gated call can change state, so the gate
// defaults to Deny and wears the amber frame. Reads are benign; writers, exec,
// process control, and unknown (MCP) tools are treated as needing care.
func approvalDestructive(toolName string) bool {
	return toolCategory[toolName] != "read"
}

// sandboxStatusLine renders the session's bash confinement as ONE line, in three
// states so a permanently-unconfined platform reads as a steady notice rather than
// a red alarm: enforce (calm green), unavailable-on-this-OS (amber), or
// deliberately-off (red). "" when unknown (e.g. in tests).
func (m chatTUI) sandboxStatusLine() string {
	switch m.bashSandbox.state {
	case "enforce":
		net := "net off"
		if m.bashSandbox.network {
			net = "net on"
		}
		return green("sandbox: enforce · writes confined · " + net)
	case "unavailable":
		return yellow("sandbox: unavailable on this OS — runs unconfined")
	default:
		// "off", "" (unknown / config-load failure), or anything unrecognised — fail
		// SAFE and warn, never silently drop the confinement line on a bash gate.
		return red("UNCONFINED — full disk + network access")
	}
}

// bashSandboxStatus is the session's bash confinement summary (see chatTUI.bashSandbox).
type bashSandboxStatus struct {
	state   string // "enforce" | "unavailable" | "off"; "" = unknown (render nothing)
	network bool
}

// bashSandboxFromConfig derives the confinement summary from the configured bash
// mode and whether the platform can actually enforce it (sandbox.Available()).
func bashSandboxFromConfig(cfg *config.Config) bashSandboxStatus {
	st := bashSandboxStatus{network: cfg.Sandbox.Network}
	switch {
	case cfg.BashMode() == "enforce" && sandbox.Available():
		st.state = "enforce"
	case cfg.BashMode() == "enforce":
		st.state = "unavailable"
	default:
		st.state = "off"
	}
	return st
}

func (m chatTUI) clampBannerLine(s string) string {
	w := m.width - 2 // left padding plus a little guard against style wrapping
	if w < 8 {
		w = 8
	}
	return clampStatusLine(s, w)
}

// approvalToolDetails turns provider-visible tool IDs into user-facing labels.
// MCP tools are advertised as mcp__<server>__<tool>; showing the short tool name
// first keeps the approval prompt readable while preserving the source.
func approvalToolDetails(toolName string) (name, detail string) {
	if server, short, ok := tool.SplitMCPName(toolName); ok {
		lines := []string{}
		if strings.EqualFold(short, "understand_image") {
			lines = append(lines, i18n.M.ToolApprovalImageUse)
		}
		lines = append(lines, fmt.Sprintf(i18n.M.ToolApprovalSourceFmt, server))
		return short, strings.Join(lines, "\n")
	}
	return toolName, fmt.Sprintf(i18n.M.ToolApprovalSourceFmt, i18n.M.ToolApprovalBuiltIn)
}

// todoPanelMaxRows caps how many task lines the pinned panel shows; a long list
// is truncated with a "+N more" footer so the bottom region stays compact.
const todoPanelMaxRows = 8

// renderTodoPanel renders the task list pinned above the input from the latest
// todo_write call (m.todoArgs): a "Tasks done/total" header, completed items
// dimmed/checked, the in-progress one highlighted (its activeForm if given),
// pending ones muted. It returns "" when there's no list or every item is done,
// so the panel appears while work is outstanding and clears itself when finished.
func (m chatTUI) renderTodoPanel() string {
	var p struct {
		Todos []struct {
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"activeForm"`
			Level      int    `json:"level"`
		} `json:"todos"`
	}
	if err := json.Unmarshal([]byte(m.todoArgs), &p); err != nil || len(p.Todos) == 0 {
		return ""
	}
	done := 0
	for _, t := range p.Todos {
		if t.Status == "completed" {
			done++
		}
	}
	if done == len(p.Todos) {
		return "" // all finished — clear the panel
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", glitchMark("TODO-STACK"), dim(fmt.Sprintf("// %d/%d", done, len(p.Todos))))
	shown := 0
	for _, t := range p.Todos {
		if shown >= todoPanelMaxRows {
			b.WriteString(dim(fmt.Sprintf("  +%d more", len(p.Todos)-shown)) + "\n")
			break
		}
		shown++
		indent := "  "
		if t.Level >= 1 {
			indent = "      " // sub-steps sit under their phase
		}
		switch t.Status {
		case "completed":
			b.WriteString(indent + green("✔") + " " + dim(t.Content) + "\n")
		case "in_progress":
			label := t.Content
			if t.ActiveForm != "" {
				label = t.ActiveForm
			}
			b.WriteString(indent + yellow("▶ "+label) + "\n")
		default:
			b.WriteString(indent + dim("○ "+t.Content) + "\n")
		}
	}
	return todoPanelStyle.Width(max(m.width, 10)).Render(strings.TrimRight(b.String(), "\n"))
}

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

// saveClipboardImageFn reads an image off the OS clipboard and returns its saved
// path; a var so tests can drive the paste→attach pipeline without a real
// clipboard (and PowerShell/osascript subprocess).
var saveClipboardImageFn = control.SaveClipboardImage

func pasteClipboardImage() tea.Cmd {
	return func() tea.Msg {
		path, err := saveClipboardImageFn()
		return clipboardImageMsg{path: path, err: err}
	}
}

func pasteClipboard() tea.Cmd {
	return func() tea.Msg {
		path, imageErr := saveClipboardImageFn()
		if imageErr == nil {
			return clipboardPasteMsg{path: path}
		}
		text, textErr := clipboard.ReadAll()
		if textErr == nil && text != "" {
			return clipboardPasteMsg{text: text}
		}
		if textErr != nil {
			return clipboardPasteMsg{err: fmt.Errorf("%v; text paste failed: %w", imageErr, textErr)}
		}
		return clipboardPasteMsg{err: imageErr}
	}
}

func (m *chatTUI) attachPastedImages(text string) bool {
	sources, ok := pastedImageSources(text)
	if !ok {
		return false
	}
	for _, src := range sources {
		path, err := savePastedImageSource(src)
		if err != nil {
			m.notice("paste image: " + err.Error())
			continue
		}
		m.insertImageRef(path)
	}
	return true
}

var markdownImageSourceRe = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)

func pastedImageSources(text string) ([]string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, false
	}
	if isDataImage(trimmed) {
		return []string{trimmed}, true
	}
	if matches := markdownImageSourceRe.FindAllStringSubmatch(trimmed, -1); len(matches) > 0 {
		rest := strings.TrimSpace(markdownImageSourceRe.ReplaceAllString(trimmed, ""))
		if rest == "" {
			sources := make([]string, 0, len(matches))
			for _, m := range matches {
				sources = append(sources, m[1])
			}
			return sources, true
		}
	}

	lines := nonEmptyPasteLines(trimmed)
	if len(lines) > 0 && allImageSources(lines) {
		return lines, true
	}
	fields := strings.Fields(trimmed)
	if len(fields) > 1 && allImageSources(fields) {
		return fields, true
	}
	return nil, false
}

func nonEmptyPasteLines(text string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func allImageSources(sources []string) bool {
	if len(sources) == 0 {
		return false
	}
	for _, src := range sources {
		if !looksLikeImageSource(src) {
			return false
		}
	}
	return true
}

func looksLikeImageSource(src string) bool {
	if isDataImage(strings.TrimSpace(src)) {
		return true
	}
	path, ok := pastedImagePath(src)
	if !ok {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

func savePastedImageSource(src string) (string, error) {
	src = strings.TrimSpace(src)
	if isDataImage(src) {
		return control.SaveImageDataURL(src)
	}
	path, ok := pastedImagePath(src)
	if !ok {
		return "", fmt.Errorf("unsupported pasted image source")
	}
	return control.SaveImageFile(path)
}

func isDataImage(src string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(src)), "data:image/")
}

func pastedImagePath(src string) (string, bool) {
	src = strings.TrimSpace(src)
	src = strings.TrimPrefix(src, "@")
	quoted := (strings.HasPrefix(src, `"`) && strings.HasSuffix(src, `"`)) || (strings.HasPrefix(src, `'`) && strings.HasSuffix(src, `'`))
	src = strings.Trim(src, "\"'")
	if src == "" {
		return "", false
	}
	if !quoted && strings.ContainsAny(src, " \t\r\n") {
		return "", false
	}
	lower := strings.ToLower(src)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return "", false
	}
	if strings.HasPrefix(lower, "file://") {
		u, err := url.Parse(src)
		if err != nil || u.Path == "" {
			return "", false
		}
		src = u.Path
	}
	if strings.HasPrefix(src, "~/") {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			src = filepath.Join(home, strings.TrimPrefix(src, "~/"))
		}
	}
	return filepath.Clean(src), true
}

// pastedFileRef turns a dragged/pasted non-image file path into an @reference so
// it attaches instead of landing as literal text (and, for a POSIX path, being
// misread as a slash command). Images are handled earlier; only path-shaped
// content (a separator) that points at a real file qualifies, so an ordinary
// pasted word is left alone.
func pastedFileRef(content string) (string, bool) {
	path, ok := pastedImagePath(content)
	if !ok || !strings.ContainsAny(path, `/\`) {
		return "", false
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", false
	}
	return "@" + path, true
}

// cycleMode toggles normal ↔ YOLO (Shift+Tab). YOLO auto-approves every tool
// call for the session (deny rules still apply). The status line's mode tag
// reflects the result.
func (m *chatTUI) cycleMode() {
	m.ctrl.SetBypass(!m.ctrl.Bypass())
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
	// Flush any half-streamed leftover before the new turn (defensive).
	m.commitReasoning()
	m.commitPending()

	// The live welcome glow is a fresh-screen flourish: the instant the conversation
	// begins, freeze it — commit the static banner to the top of the transcript so it
	// becomes normal scrollback and the viewport takes over from the live render.
	if m.bannerLive {
		m.bannerLive = false
		staticBanner := strings.TrimRight(renderTUIBanner(m.label, m.missing, m.width), "\n")
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
	m.ctrl.SendWithRaw(sent, raw)
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
	case event.Reasoning:
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
		m.commitReasoning() // reasoning ends as the answer begins
		m.pending.WriteString(e.Text)
		m.streamAnswer()

	case event.Message:
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
		switch e.Tool.Name {
		case "todo_write":
			// Drive the pinned task list above the input (renderTodoPanel) rather
			// than printing a tool line; it updates in place as the list evolves.
			m.todoArgs = e.Tool.Args
		default:
			m.commitSpacer()
			if block := diffBlock(e.Tool.Name, e.Tool.Args, e.Tool.FileDiff, m.width, diffScrollbackMaxLines); block != nil {
				for _, ln := range block {
					m.commitLine(surfaceWrap(ln, m.width))
				}
				break
			}
			m.commitLine(surfaceWrap(toolCard(e.Tool.Name, e.Tool.Args, m.width), m.width))
			m.beginToolRunning(e.Tool.ID)
		}

	case event.ToolProgress:
		m.streamToolOutput(e.Tool.ID, e.Tool.Output)

	case event.ToolResult:
		// A successful result is silent (it only feeds the model); a blocked/failed
		// call surfaces a red "● Verb ⊘ <reason>" card. A live-output block (bash)
		// collapses to a one-line "╰─ N lines" summary first.
		m.collapseToolOutput(e.Tool.ID)
		if e.Tool.Err != "" {
			m.finalizeStreamed()
			m.commitLine(surfaceWrap("  "+red("●")+" "+bold(toolDisplayName(e.Tool.Name))+" "+red("⊘ "+e.Tool.Err), m.width))
		}

	case event.Usage:
		if e.Usage != nil {
			m.turnTokens += e.Usage.CompletionTokens
		}
		if line := usageLine(e.Usage, e.Pricing); line != "" {
			m.finalizeStreamed()
			m.commitLine(line)
		}

	case event.Notice:
		glyph := "·"
		if e.Level == event.LevelWarn {
			glyph = "!"
		}
		m.finalizeStreamed()
		m.commitLine(fmt.Sprintf("  %s %s", glyph, e.Text))

	case event.CompactionStarted:
		m.finalizeStreamed()
		m.commitLine(dim("  ⋯ " + i18n.M.CompactionWorking))

	case event.CompactionDone:
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
		m.finalizeStreamed()
		m.commitLine(fmt.Sprintf("[%s]", e.Text))

	case event.ApprovalRequest:
		// The controller's run goroutine is now blocked inside the gate awaiting
		// this decision; the banner shows it in View and key input answers it via
		// ctrl.Approve. At most one prompt is outstanding (the controller
		// serialises them), so a plain field holds the current one.
		a := e.Approval
		m.pendingApproval = &a
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
		// The `ask` tool raised a question card; the run goroutine blocks until
		// ctrl.AnswerQuestion resolves it. Keys drive the card while it's set.
		m.finalizeStreamed()
		m.chooser = newChooser(e.Ask)

	case event.TurnDone:
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
		m.clearSubmittedPastes()
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

func waitForAgentEvent(ch chan event.Event) tea.Cmd {
	return func() tea.Msg { return agentEventMsg(<-ch) }
}

func elapsedTick() tea.Cmd {
	return tea.Tick(time.Second, func(_ time.Time) tea.Msg { return elapsedTickMsg{} })
}

// bannerFrameMsg is one welcome-banner glow frame.
type bannerFrameMsg struct{}

// bannerFrameTick paces the welcome-banner glow at ~60fps. It only re-schedules while
// the banner is live (the fresh welcome screen), so the high frame rate never persists
// into a chat; each frame just advances a phase and re-renders a tiny string, so there's
// no memory growth — the banner is recomputed in place, never accumulated.
func bannerFrameTick() tea.Cmd {
	return tea.Tick(time.Second/60, func(_ time.Time) tea.Msg { return bannerFrameMsg{} })
}

// runSlashCommand handles "/<cmd> <args>" input. Local commands queue their
// output to scrollback; MCP prompt / custom commands resolve to a model turn.
func (m *chatTUI) runSlashCommand(input string) tea.Cmd {
	cmd := strings.TrimSpace(strings.SplitN(input, " ", 2)[0])

	if strings.HasPrefix(cmd, "/mcp__") {
		return m.runMCPPrompt(input)
	}

	switch cmd {
	case "/compact":
		m.echoLocalCommand(input)
		// Compaction makes a (network) summarizer call; run it off the Update loop
		// so the TUI doesn't freeze. The CompactionStarted/Done events render the
		// card as they arrive; compactDoneMsg only handles the terminal error /
		// snapshot once the pass returns. Any text after "/compact" is focus
		// guidance steering what the summary keeps.
		focus := strings.TrimSpace(strings.TrimPrefix(input, cmd))
		return func() tea.Msg { return compactDoneMsg{err: m.ctrl.Compact(context.Background(), focus)} }
	case "/new":
		m.echoLocalCommand(input)
		if err := m.ctrl.NewSession(); err != nil {
			m.notice(fmt.Sprintf("%s: %v", i18n.M.SlashNewFailed, err))
			return nil
		}
		// Native scrollback keeps the old transcript; mark the fork with a fresh
		// banner and reset live state.
		m.pending.Reset()
		m.reasoning.Reset()
		m.todoArgs = ""
		m.chooser = nil
		m.commitLine("")
		m.commitLine(strings.TrimRight(renderTUIBanner(m.label, "", m.width), "\n"))
		m.notice(i18n.M.SlashNewDone)
	case "/resume":
		m.runResumeCommand(input)
	case "/todo":
		m.echoLocalCommand(input)
		// Dismiss the pinned task list; a later todo_write brings it back.
		m.todoArgs = ""
		m.notice(i18n.M.SlashTodoCleared)
	case "/verbose":
		m.toggleVerboseReasoning(true)
	case "/effort":
		return m.runEffortCommand(input)
	case "/rewind":
		m.echoLocalCommand(input)
		m.openRewind()
	case "/tree":
		m.echoLocalCommand(input)
		m.showBranchTree()
	case "/branch":
		m.echoLocalCommand(input)
		m.runBranchCommand(input)
	case "/switch":
		m.echoLocalCommand(input)
		m.runSwitchCommand(input)
	case "/mcp":
		m.echoLocalCommand(input)
		m.runMCPSubcommand(input)
	case "/model":
		m.echoLocalCommand(input)
		m.runModelSubcommand(input)
		if m.pendingModelSwitch != nil {
			return m.pendingModelSwitch
		}
	case "/skill", "/skills":
		m.echoLocalCommand(input)
		m.runSkillSubcommand(input)
	case "/hooks":
		m.echoLocalCommand(input)
		m.runHooksSubcommand(input)
	case "/paste-image":
		return pasteClipboardImage()
	case "/output-style", "/output-styles":
		m.echoLocalCommand(input)
		styles := outputstyle.List(outputstyle.Dirs())
		if len(styles) == 0 {
			m.notice(i18n.M.OutputStyleNone)
		} else {
			m.commitLine(renderOutputStyles(m.width, styles, m.outputStyle))
		}
	case "/theme":
		m.echoLocalCommand(input)
		m.runThemeSubcommand(input)
	case "/help":
		m.echoLocalCommand(input)
		m.showHelp()
	case "/memory":
		m.echoLocalCommand(input)
		m.showMemory()
	case "/quit", "/exit":
		return tea.Quit
	case "/forget":
		m.forgetMemory(strings.TrimSpace(strings.TrimPrefix(input, cmd)))
	case "/goal":
		arg := strings.TrimSpace(strings.TrimPrefix(input, cmd))
		switch {
		case arg == "":
			m.echoLocalCommand(input)
			if g, n := m.ctrl.Goal(); g != "" {
				m.notice(fmt.Sprintf("● goal active (×%d): %s", n, g))
			} else {
				m.notice(i18n.M.GoalNone)
			}
		case arg == "clear" || arg == "off":
			m.echoLocalCommand(input)
			if g, _ := m.ctrl.Goal(); g != "" {
				m.ctrl.ClearGoal()
				m.notice(i18n.M.GoalCleared)
			} else {
				m.notice(i18n.M.GoalNone)
			}
		default:
			// Arm the goal, then kick the first turn (unless one is already running —
			// then the in-flight turn picks it up at its tail). The bare condition is
			// the model's opening task; runGoalLoop takes over from the turn's end.
			m.ctrl.SetGoal(arg)
			m.notice(fmt.Sprintf(i18n.M.GoalSetFmt, arg))
			if !m.ctrl.Running() {
				return m.startTurn(arg, input, input)
			}
			m.echoLocalCommand(input)
		}
	default:
		// A custom command wins over a skill of the same name; both resolve to a turn.
		if sent, ok := m.ctrl.CustomCommand(input); ok {
			return m.startTurn(sent, input, input)
		}
		if sent, ok := m.ctrl.RunSkill(input); ok {
			return m.startTurn(sent, input, input)
		}
		m.notice(fmt.Sprintf("%s: %s", i18n.M.SlashUnknown, cmd))
	}
	return nil
}

func (m *chatTUI) echoLocalCommand(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}
	m.commitLine(dim("  › " + input))
}

// commandNames renders the custom command list for /help, "" when there are none.
func (m *chatTUI) commandNames() string {
	if len(m.commands) == 0 {
		return ""
	}
	names := make([]string, len(m.commands))
	for i, c := range m.commands {
		names[i] = "/" + c.Name
	}
	return strings.Join(names, " · ")
}

// runMCPSubcommand handles "/mcp" (status), "/mcp add …" (connect a server live
// and persist it), and "/mcp remove <name>" (disconnect + drop from config). Add
// connects synchronously — like /compact, an explicit command may briefly block
// the UI while the handshake runs.
func (m *chatTUI) runMCPSubcommand(input string) {
	args := tokenizeArgs(input) // args[0] == "/mcp"
	if len(args) < 2 {
		m.showMCPStatus()
		return
	}
	switch args[1] {
	case "list", "ls":
		// The completion menu offers "list"; treat it as the status view (same as
		// a bare /mcp) rather than an unknown subcommand.
		m.showMCPStatus()
	case "add":
		entry, err := parseMCPAdd(args[2:])
		if err != nil {
			m.notice(err.Error())
			return
		}
		n, err := m.ctrl.AddMCPServer(entry)
		if err != nil {
			m.notice("mcp add: " + err.Error())
			return
		}
		m.notice(fmt.Sprintf("connected %s — %d tools, saved to config (available next message)", entry.Name, n))
	case "connect":
		if len(args) < 3 {
			m.notice("usage: /mcp connect <name>")
			return
		}
		n, err := m.ctrl.ConnectConfiguredMCPServer(args[2])
		if err != nil {
			m.notice("mcp connect: " + err.Error())
			return
		}
		m.host = m.ctrl.Host()
		m.notice(fmt.Sprintf("connected %s — %d tools (available next message)", args[2], n))
	case "remove", "rm":
		if len(args) < 3 {
			m.notice("usage: /mcp remove <name>")
			return
		}
		name := args[2]
		disconnected, err := m.ctrl.RemoveMCPServer(name)
		if err != nil {
			m.notice("mcp remove: " + err.Error())
			return
		}
		if disconnected {
			m.notice("disconnected " + name + " and removed it from config")
		} else {
			m.notice("removed " + name + " from config")
		}
	default:
		m.notice("unknown /mcp subcommand " + args[1] + " — try: /mcp, /mcp add, /mcp connect, /mcp remove")
	}
}

// showMCPStatus queues the connected MCP servers, their counts, and the prompt
// commands / resource refs they expose — the discovery surface for /mcp.
func (m *chatTUI) showMCPStatus() {
	if m.host == nil || (len(m.host.Servers()) == 0 && len(m.host.Failures()) == 0) {
		m.notice(i18n.M.SlashMCPNone)
		return
	}
	m.commitLine(renderMCPStatus(m.width, m.host.Servers(), m.host.Prompts(), m.host.Resources(), m.host.Failures()))
}

// notice queues a dim informational line to scrollback.
func (m *chatTUI) notice(note string) {
	m.commitLine(dim("  · " + note))
}

// resolveRefs resolves a line's @references off the event loop via the
// controller, delivering a refsResolvedMsg with the tagged context block.
func (m *chatTUI) resolveRefs(sent, display, restore string) tea.Cmd {
	return func() tea.Msg {
		block, errs := m.ctrl.ResolveRefs(context.Background(), sent)
		return refsResolvedMsg{sent: sent, display: display, restore: restore, block: block, errs: errs}
	}
}

// runMCPPrompt resolves a /mcp__server__prompt command off the event loop via
// the controller, delivering a promptResolvedMsg with the rendered prompt.
func (m *chatTUI) runMCPPrompt(input string) tea.Cmd {
	return func() tea.Msg {
		sent, found, err := m.ctrl.MCPPrompt(context.Background(), input)
		if !found {
			name := strings.TrimPrefix(strings.Fields(input)[0], "/")
			return promptResolvedMsg{display: input, err: fmt.Errorf("%s: /%s", i18n.M.SlashUnknown, name)}
		}
		return promptResolvedMsg{display: input, sent: sent, err: err}
	}
}

// replaySectionsFor turns a loaded session into scrollback blocks: user bubbles
// and assistant markdown. Tool messages are dropped — needed in session state
// but noise in the visible transcript on resume.
func replaySectionsFor(history []provider.Message, width int, renderer *mdRenderer) []string {
	var out []string
	for _, m := range history {
		switch m.Role {
		case provider.RoleUser:
			out = append(out, renderUserBubble(m.Content, width)+"\n\n")
		case provider.RoleAssistant:
			body := strings.TrimSpace(m.Content)
			if body == "" {
				continue
			}
			rendered := renderer.Render(body)
			if rendered == "" {
				rendered = body
			}
			out = append(out, rendered+"\n")
		}
	}
	return out
}

// renderTUIBanner is the title + tip + optional missing-key warning printed once
// at the top of the session.
func renderTUIBanner(label, missing string, width int) string {
	return renderTUIBannerAt(label, missing, width, -1)
}

// renderTUIBannerAt renders the startup banner; phase >= 0 sweeps the hero wordmark
// with the ambient glow at that animation phase, phase < 0 is the frozen static art.
func renderTUIBannerAt(label, missing string, width, phase int) string {
	if width <= 0 {
		width = 80
	}
	var b strings.Builder
	const indent = "  "
	// A warm ambient ramp — copper glow → amber → seafoam — reused for the hero art
	// and the compact wordmark so the brand reads the same at every width. (Warm,
	// lamplit; not the old neon copper→cyan→magenta.)
	stops := []cliColor{activeCLITheme.accent, activeCLITheme.warn, activeCLITheme.toolRead}
	artW := roachArtWidth()

	b.WriteString("\n") // a little breathing room above the wordmark

	if width >= artW+len(indent)+1 {
		// Hero: the ROACH wordmark with a diagonal light axis; phase >= 0 adds the
		// slow ambient glow sweep (the static-art analogue of the thinking shimmer).
		heroRows := roachHeroRows(stops)
		if phase >= 0 {
			heroRows = roachHeroShimmerRows(stops, phase)
		}
		for _, row := range heroRows {
			b.WriteString(indent + row + "\n")
		}
		b.WriteString("\n") // one row of air under the wordmark before the subtitle
		sub := indent + accent("roach·code") + dim("  //  "+label)
		b.WriteString(clampStatusLine(sub, width) + "\n")
		b.WriteString(clampStatusLine(indent+dim("a coding harness for token optimization"), width) + "\n")
		b.WriteString(clampStatusLine(indent+dim(i18n.M.ChatTip), width) + "\n")
	} else {
		// Too narrow for the art: a single gradient wordmark carries the brand.
		title := gradient("roach·code", true, stops...) + dim("  // "+label)
		b.WriteString(clampStatusLine(indent+title, width) + "\n")
		b.WriteString(clampStatusLine(indent+dim(i18n.M.ChatTip), width) + "\n")
	}
	if missing != "" {
		b.WriteString(wrapForViewport(indent+"! "+missing, width, activeCLITheme.warn) + "\n")
	}
	return b.String()
}

// wrapForViewport hard-wraps text to fit width columns and colours every line.
func wrapForViewport(text string, width int, fg cliColor) string {
	if width <= 0 {
		width = 80
	}
	return themeStyle(fg).Width(width).Render(text)
}

// renderUserBubble styles the just-submitted line with a filled dim background.
func renderUserBubble(line string, width int) string {
	line = displayLineForImageRefs(line)
	prefix := "› "
	if !colorEnabled {
		return "│ " + prefix + line
	}
	w := width - 4
	if w < 10 {
		w = 10
	}
	bubble := themeBGStyle(activeCLITheme.userBubbleBG).Width(w).Padding(0, 1)
	return bubble.Render(prefix + line)
}

var cliImageRefRe = regexp.MustCompile(`(?:^|\s)@\.roach-code/attachments/clipboard-\d{8}-\d{6}\.\d+(?:-(?:\d{6}|[a-f0-9]{8}))?\.(?:png|jpg|jpeg|gif|webp)`)

func displayLineForImageRefs(line string) string {
	idx := 0
	out := cliImageRefRe.ReplaceAllStringFunc(line, func(_ string) string {
		idx++
		return " [image" + strconv.Itoa(idx) + "]"
	})
	return strings.TrimSpace(out)
}

// eventSink is the event.Sink the agent emits to in TUI mode. Each event
// becomes an agentEventMsg. The channel is generously buffered so streaming
// bursts don't back-pressure the agent goroutine.
type eventSink struct {
	ch chan<- event.Event
}

func (s *eventSink) Emit(e event.Event) { s.ch <- e }
