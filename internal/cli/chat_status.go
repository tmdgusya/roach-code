package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"roach-code/internal/event"
	"roach-code/internal/hook"
	"roach-code/internal/i18n"
)

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

// jobsTag mirrors Qwen's background-task pill in the built-in status line:
// active jobs take priority, while retained terminal jobs leave a quiet "done"
// affordance so /jobs remains discoverable after work finishes.
func (m chatTUI) jobsTag() string {
	if m.ctrl == nil {
		return ""
	}
	views := m.ctrl.AllJobs()
	if len(views) == 0 {
		return ""
	}
	running := 0
	for _, j := range views {
		if j.Status == "running" {
			running++
		}
	}
	if running > 0 {
		return themeFg(activeCLITheme.faint, fmt.Sprintf("%d %s", running, plural(running, "job", "jobs")))
	}
	return themeFg(activeCLITheme.faint, fmt.Sprintf("%d %s done", len(views), plural(len(views), "job", "jobs")))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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

// shortTPS prints tokens-per-second compactly: 3500 → "3.5K", 12000 → "12.0K".
// Unlike shortTokens, it preserves one decimal for the sub-thousand fraction.
func shortTPS(n int) string {
	switch {
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
