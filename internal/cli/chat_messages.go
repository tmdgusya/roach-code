package cli

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"roach-code/internal/command"
	"roach-code/internal/control"
	"roach-code/internal/event"
	"roach-code/internal/plugin"
	"roach-code/internal/provider"
	"roach-code/internal/skill"
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
	msg     provider.Message
	display string
	restore string
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

// ultragoalFrameMsg is one frame of the idle ultragoal preview shimmer.
type ultragoalFrameMsg struct{}

// ultragoalFrameTick paces the ultragoal preview banner's shimmer at ~30fps while
// the composer holds the keyword. Like bannerFrameTick it only reschedules while
// previewing, so the frame rate never persists past the preview.
func ultragoalFrameTick() tea.Cmd {
	return tea.Tick(time.Second/30, func(_ time.Time) tea.Msg { return ultragoalFrameMsg{} })
}

// updateCheckMsg carries the result of an async latest-release check.
type updateCheckMsg struct {
	latest string
	err    error
}

// checkUpdateCmd fetches the latest GitHub release tag off the event loop.
// It respects a 3-hour on-disk cache so frequent restarts don't hammer the
// GitHub API. It silently returns an empty latest on error so the banner never
// surfaces noise for offline or slow networks.
func checkUpdateCmd(version string) tea.Cmd {
	return func() tea.Msg {
		if latest, ok := readUpdateCache(); ok {
			if versionNewer(version, latest) {
				return updateCheckMsg{latest: latest}
			}
			return updateCheckMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		latest, err := latestReleaseTag(ctx, repoSlug())
		if err != nil || latest == "" {
			return updateCheckMsg{}
		}
		writeUpdateCache(latest)
		if !versionNewer(version, latest) {
			return updateCheckMsg{}
		}
		return updateCheckMsg{latest: latest}
	}
}

// eventSink is the event.Sink the agent emits to in TUI mode. Each event
// becomes an agentEventMsg. The channel is generously buffered so streaming
// bursts don't back-pressure the agent goroutine.
type eventSink struct {
	ch chan<- event.Event
}

func (s *eventSink) Emit(e event.Event) { s.ch <- e }
