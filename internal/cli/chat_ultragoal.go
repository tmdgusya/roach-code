package cli

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Ultragoal is roach-code's "dynamic workflow" trigger, the analogue of typing a
// keyword that lights up and engages a richer mode. Typing `ultragoal <goal>`
// (or `/ultragoal <goal>`) in chat steers the turn to accomplish <goal> via the
// run_workflow tool — one model call that fans out into many sub-agents whose
// intermediate results stay in script variables — and glows a sparkle banner
// while it runs.

// ultragoalKeywords are the trigger words. A line that is (case-insensitively)
// exactly one of these, or starts with one followed by a space, engages it.
var ultragoalKeywords = []string{"ultragoal", "/ultragoal"}

// ultragoalTask reports whether line triggers ultragoal, returning the goal text
// after the keyword ("" when the keyword was typed alone).
func ultragoalTask(line string) (task string, ok bool) {
	l := strings.TrimSpace(line)
	low := strings.ToLower(l)
	for _, kw := range ultragoalKeywords {
		if low == kw {
			return "", true
		}
		if strings.HasPrefix(low, kw+" ") {
			return strings.TrimSpace(l[len(kw):]), true
		}
	}
	return "", false
}

// ultragoalPreamble steers the model to accomplish the goal via a single
// run_workflow call rather than doing the work inline — the point of the mode.
// It is prepended to the user's goal as the model input; the transcript shows
// only the clean goal.
const ultragoalPreamble = `[ULTRAGOAL — dynamic workflow mode]
Accomplish the goal below by calling the run_workflow tool exactly once. Do not do the work yourself in this conversation.

Write a JavaScript workflow that plans first, investigates before acting, then executes against that plan.

The script must begin by defining a compact plan object or array in code. The plan must name:
- phases: investigation, execution, verification, synthesis as applicable;
- work units: what each unit does, whether it can run in parallel, its dependencies, and its expected output;
- investigation needs: unknown code paths, behavior, APIs, tests, external facts, or risks that must be mapped before implementation/final judgment;
- done criteria: the verification evidence required for each unit to count as complete.

Use that plan to build the workflow graph:
- Run investigation units first when the goal depends on unknowns. Use focused read-only agents to map relevant files/symbols/contracts/tests and return file:line or source evidence.
- Use parallel() only for genuinely independent units. Use pipeline() or ordinary await sequencing when a unit depends on earlier findings or changes.
- Each agent(prompt, opts) prompt must be specific and self-contained: include the unit's scope, inputs from earlier units, exact deliverable, evidence to return, and verification required.
- For code changes, require agents to inspect surrounding code before editing, keep changes minimal, and avoid unrelated refactors.

Verification levels:
- Research/analysis units: cite the searches/files/sources checked, and state negative findings with the searches used.
- Code-change units: verify with the narrowest relevant test/build/linter command, plus broader package/project tests when the change is cross-cutting or risky.
- Security/input/file/network/auth changes: include a security-focused check or review and call out unresolved risk.
- If verification cannot be run, the result must say exactly why and what command/check should be run next; do not mark that unit complete.

Keep intermediate findings in script variables, not your context window. End the script by returning a synthesized final result that includes the decomposition plan used, investigation evidence, what was changed or learned, verification evidence, failures or skipped checks, and remaining caveats. Then report that result to me.

Goal:`

// startUltragoal engages dynamic-workflow mode for task: it lights the sparkle
// banner and starts a turn whose model input is the steering preamble + task,
// while the transcript shows only the clean goal.
func (m *chatTUI) startUltragoal(task string) tea.Cmd {
	m.ultragoalActive = true
	sent := ultragoalPreamble + "\n\n" + task
	displayed := "✨ ultragoal: " + task
	restore := "ultragoal " + task
	return m.startTurn(sent, displayed, restore)
}

// ultragoalPreviewing reports whether the composer currently holds the keyword
// (so the input box should glow), and a turn isn't already running.
func (m chatTUI) ultragoalPreviewing() bool {
	if m.state == tuiRunning {
		return false
	}
	_, ok := ultragoalTask(m.input.Value())
	return ok
}

// ultragoalGlowing reports whether the input box should shimmer right now: while
// the composer holds the keyword (idle preview), or while the engaged turn runs.
func (m chatTUI) ultragoalGlowing() bool {
	return m.ultragoalActive || m.ultragoalPreviewing()
}

// ultragoalGlowPhase is the shimmer phase for the input-box glow: the idle
// preview tick while typing the keyword, the spinner tick while the turn runs.
func (m chatTUI) ultragoalGlowPhase() int {
	if m.ultragoalActive {
		return m.shimmerPhase
	}
	return m.ultragoalPhase
}

// shimmerInputBox makes the composer glow when ultragoal is armed or running:
// every line's text — the typed characters AND the top/bottom rules — is
// repainted with a travelling shimmer crest. Each line is ANSI-stripped to its
// plain glyphs first (the textarea uses the real terminal cursor, SetVirtualCursor
// false, so no cursor cell lives in the text to lose), then shimmered over just
// its visible span so the wave sweeps the letters rather than the trailing pad.
// shimmer preserves per-rune width, so the cursor position is unchanged. No-op
// under NO_COLOR / a non-tty.
func shimmerInputBox(box string, phase int) string {
	if !colorEnabled {
		return box
	}
	lines := strings.Split(box, "\n")
	for i, ln := range lines {
		plain := ansi.Strip(ln)
		trimmed := strings.TrimRight(plain, " ")
		if trimmed == "" {
			continue // blank/padding-only line: leave it be
		}
		lines[i] = shimmer(trimmed, phase) + plain[len(trimmed):] // keep trailing pad for width
	}
	return strings.Join(lines, "\n")
}
