package cli

import (
	"regexp"
	"strconv"
	"strings"

	"roach-code/internal/i18n"
	"roach-code/internal/provider"
)

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
