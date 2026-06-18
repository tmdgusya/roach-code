package cli

import (
	"fmt"
	"strings"

	"roach-code/internal/agent"
	"roach-code/internal/provider"
)

// usageLine is the cli-themed, foreground-only twin of agent.FormatUsageLine,
// committed to chat scrollback after every assistant turn. The shared headless
// TextSink keeps agent.FormatUsageLine (plain) — this themed version is the chat
// TUI's only.
//
// Design (high-taste pass, terminal-native): the line answers one question per
// turn — "did the cache carry this turn, or did it pay full price?" — told by
// WEIGHT, not a percentage or a bar. Everything is dim faint EXCEPT the `new`
// (cache-miss) token count, which is bold muted: a small bold number next to a
// long dim `cached` run means the prefix held; a fat bold `new` means a cold turn.
// One lone accent tick leads the line; no background, no animation (it repeats
// forever, so it must read quiet). The grand total and the reasoning sub-count
// were dropped — both are derivable/secondary and competed with the headline.
func usageLine(u *provider.Usage, p *provider.Pricing, tps int) string {
	if u == nil || u.TotalTokens == 0 {
		return ""
	}
	if !colorEnabled { // NO_COLOR / piped → byte-identical plain fallback
		return agent.FormatUsageLine(u, p)
	}
	fa := func(s string) string { return themeFg(activeCLITheme.faint, s) }

	var b strings.Builder
	b.WriteString("  " + accent("·") + " ") // the one warm tick that marks the line
	b.WriteString(fa("in "+shortTokens(u.PromptTokens)) + "  ")

	// cache group: cached dim (left), new bold-muted (right). The relative mass of
	// the two runs IS the cache gauge — no second hue, no bar, no background.
	cached := u.CacheHitTokens
	fresh := u.CacheMissTokens
	if fresh == 0 {
		if d := u.PromptTokens - cached; d > 0 {
			fresh = d
		}
	}
	b.WriteString(fa(shortTokens(cached) + " cached"))
	b.WriteString(fa(" · "))
	b.WriteString(bold(themeFg(activeCLITheme.muted, shortTokens(fresh))) +
		themeFg(activeCLITheme.muted, " new"))

	b.WriteString(fa(fmt.Sprintf("  · out %d", u.CompletionTokens)))
	if p != nil {
		b.WriteString(fa(fmt.Sprintf("  · %s%.4f", p.Symbol(), p.Cost(u))))
	}
	if tps > 0 {
		b.WriteString(fa(fmt.Sprintf("  @ %s/s", shortTPS(tps))))
	}
	return b.String()
}
