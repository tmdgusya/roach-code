package cli

import (
	"fmt"
	"image/color"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"roach-code/internal/textarea"

	"roach-code/internal/i18n"
)

type cliColor struct {
	hex   string
	xterm int
}

type cliPalette struct {
	name         string
	style        string
	accent       cliColor
	muted        cliColor
	faint        cliColor
	success      cliColor
	warn         cliColor
	err          cliColor
	danger       cliColor
	border       cliColor
	selection    cliColor
	userBubbleBG cliColor // retained for compat; no longer used as a fill post-amp-redesign
	diffAddBG    cliColor
	diffDelBG    cliColor
	toolRead     cliColor
	toolProc     cliColor
	surface      cliColor // neutral panel fill behind tool/output blocks (ambient depth)
	surfaceLift  cliColor // the panel's top row — one step toward the light
	surfaceSeam  cliColor // 1-cell lit edge at col 3, where the lamp catches the structural spine
	text         cliColor // base body-text foreground (neutral off-white, not terminal default)
	ink          cliColor // page background painted across the whole alt-screen (the ambient canvas)
}

type cliThemeStyle struct {
	name        string
	mode        string
	accent      cliColor
	description string
}

var (
	// The default dark shell is "amp": a black canvas, low-contrast neutral grey
	// text, and a sparse muted green accent. The colour is deliberately quieter
	// than the previous blue pass so the TUI reads closer to Amp's terminal-native
	// welcome screen: black first, muted text second, accent only where it matters.
	cliDarkTheme = cliPalette{
		name:         "dark",
		style:        "amp",
		accent:       cliColor{"#48a36d", 65},  // muted green accent, used sparingly
		muted:        cliColor{"#8a8a8a", 245}, // normal labels and secondary body
		faint:        cliColor{"#5f5f5f", 240}, // meta/help/footer text
		success:      cliColor{"#5ba870", 65},  // subdued green
		warn:         cliColor{"#b58a45", 136}, // subdued amber
		err:          cliColor{"#c85f5f", 131}, // subdued red
		danger:       cliColor{"#d06464", 167}, // destructive emphasis
		border:       cliColor{"#1a1a1a", 234}, // quiet input/separator rule
		selection:    cliColor{"#48a36d", 65},
		userBubbleBG: cliColor{"#080808", 232}, // retained for compat; not used as fill
		diffAddBG:    cliColor{"#0b1a10", 22},  // very dark green
		diffDelBG:    cliColor{"#1e0d0d", 52},  // very dark red
		toolRead:     cliColor{"#8a8a8a", 245}, // tool labels stay neutral
		toolProc:     cliColor{"#6f6f6f", 242}, // process/tool secondary
		surface:      cliColor{"#080808", 232}, // rare elevated rows only
		surfaceLift:  cliColor{"#101010", 233}, // subtle local lift
		surfaceSeam:  cliColor{"#181818", 234}, // separators
		text:         cliColor{"#d7d7d7", 188}, // high-emphasis text
		ink:          cliColor{"#000000", 16},  // Amp-like black canvas
	}
	cliLightTheme = cliPalette{
		name:         "light",
		style:        "amp",
		accent:       cliColor{"#357fa8", 74},  // steel-blue accent (single brand colour)
		muted:        cliColor{"#52525b", 240}, // neutral grey
		faint:        cliColor{"#a1a1aa", 145}, // dim neutral grey
		success:      cliColor{"#16a34a", 34},  // neutral green
		warn:         cliColor{"#d97706", 172}, // neutral amber
		err:          cliColor{"#dc2626", 124}, // neutral red
		danger:       cliColor{"#e5484d", 167}, // alarm red
		border:       cliColor{"#e4e4e7", 254}, // neutral light grey border
		selection:    cliColor{"#357fa8", 74},
		userBubbleBG: cliColor{"#f4f4f5", 255}, // neutral panel (unused for fill post-redesign; kept for compat)
		diffAddBG:    cliColor{"#ecfdf5", 255}, // neutral-tinted light green
		diffDelBG:    cliColor{"#fef2f2", 255}, // neutral-tinted light red
		toolRead:     cliColor{"#357fa8", 74},  // steel-blue
		toolProc:     cliColor{"#64748b", 66},  // cool slate-grey
		surface:      cliColor{"#f4f4f5", 255}, // neutral paper panel fill
		surfaceLift:  cliColor{"#fafafa", 255}, // top row, one step lighter
		surfaceSeam:  cliColor{"#ffffff", 255}, // lit edge at col 3
		text:         cliColor{"#27272a", 235}, // neutral near-black body text
		ink:          cliColor{"#fafafa", 255}, // neutral near-white page canvas
	}
	cliThemeStyles = []cliThemeStyle{
		{name: "amp", mode: "dark", accent: cliColor{"#48a36d", 65}, description: "near-black muted green (amp)"},
		{name: "amp", mode: "light", accent: cliColor{"#357fa8", 74}, description: "minimal steel-blue (amp)"},
	}
	activeCLITheme                  = applyCLIThemeStyle(cliDarkTheme, cliThemeStyles[0])
	queryTerminalBackgroundForTheme = queryTerminalBackground
)

func configureCLITheme(mode string) {
	configureCLIThemeWithStyle(mode, "")
}

func configureCLIThemeWithStyle(mode, style string) {
	if env := strings.TrimSpace(os.Getenv("ROACH_THEME")); env != "" {
		if st, ok := cliThemeStyleByName(env); ok {
			mode = st.mode
			style = st.name
		} else {
			mode = env
		}
	}
	if env := strings.TrimSpace(os.Getenv("ROACH_THEME_STYLE")); env != "" {
		style = env
	}
	activeCLITheme = resolveCLIThemeWithStyle(mode, style)
	refreshCLIStyles()
}

func resolveCLITheme(mode string) cliPalette {
	return resolveCLIThemeWithStyle(mode, "")
}

func resolveCLIThemeWithStyle(mode, style string) cliPalette {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if st, ok := cliThemeStyleByName(mode); ok {
		return buildCLITheme(st.mode, st.name)
	}
	resolvedMode := resolveCLIThemeMode(mode)
	st, ok := cliThemeStyleByName(style)
	if !ok || st.mode != resolvedMode {
		st = defaultCLIThemeStyle(resolvedMode)
	}
	return buildCLITheme(resolvedMode, st.name)
}

func resolveCLIThemeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "light":
		return "light"
	case "dark":
		return "dark"
	case "auto", "":
		if rgb, ok := queryTerminalBackgroundForTheme(); ok {
			if rgb.looksLight() {
				return "light"
			}
			return "dark"
		}
		if colorFGBGLooksLight() {
			return "light"
		}
		return "dark"
	default:
		return "dark"
	}
}

func buildCLITheme(mode, style string) cliPalette {
	base := cliDarkTheme
	if mode == "light" {
		base = cliLightTheme
	}
	st, ok := cliThemeStyleByName(style)
	if !ok || st.mode != base.name {
		st = defaultCLIThemeStyle(base.name)
	}
	return applyCLIThemeStyle(base, st)
}

func applyCLIThemeStyle(base cliPalette, style cliThemeStyle) cliPalette {
	base.style = style.name
	base.accent = style.accent
	base.selection = style.accent
	return base
}

func cliThemeStyleByName(name string) (cliThemeStyle, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, st := range cliThemeStyles {
		if st.name == name {
			return st, true
		}
	}
	return cliThemeStyle{}, false
}

func defaultCLIThemeStyle(mode string) cliThemeStyle {
	// Both modes now share the single "amp" tone; pick the matching-mode entry.
	for _, st := range cliThemeStyles {
		if st.mode == mode {
			return st
		}
	}
	return cliThemeStyles[0]
}

// withoutTerminalProbe resolves a theme with the OSC background probe disabled —
// for callers running while something else (the live TUI) owns stdin, where a
// raw-mode read would fight the TUI's input reader. "auto" then falls back to the
// COLORFGBG heuristic.
func withoutTerminalProbe(fn func()) {
	prev := queryTerminalBackgroundForTheme
	queryTerminalBackgroundForTheme = func() (terminalRGB, bool) { return terminalRGB{}, false }
	defer func() { queryTerminalBackgroundForTheme = prev }()
	fn()
}

func setCLIThemeMode(mode string) cliPalette {
	// A runtime /theme switch runs inside the TUI, which owns stdin, so resolving
	// "auto" must not live-probe the terminal here.
	withoutTerminalProbe(func() {
		activeCLITheme = resolveCLIThemeWithStyle(mode, activeCLITheme.style)
	})
	refreshCLIStyles()
	return activeCLITheme
}

func setCLIThemeStyle(name string) (cliPalette, bool) {
	st, ok := cliThemeStyleByName(name)
	if !ok {
		return cliPalette{}, false
	}
	activeCLITheme = resolveCLIThemeWithStyle(st.mode, st.name)
	refreshCLIStyles()
	return activeCLITheme, true
}

type terminalRGB struct {
	r int
	g int
	b int
}

func (c terminalRGB) looksLight() bool {
	luma := 0.2126*float64(c.r) + 0.7152*float64(c.g) + 0.0722*float64(c.b)
	return luma >= 150
}

func parseOSC11Response(s string) (terminalRGB, bool) {
	idx := strings.Index(s, "]11;")
	if idx < 0 {
		return terminalRGB{}, false
	}
	payload := s[idx+len("]11;"):]
	if end := strings.IndexByte(payload, '\a'); end >= 0 {
		payload = payload[:end]
	} else if end := strings.Index(payload, "\x1b\\"); end >= 0 {
		payload = payload[:end]
	}
	payload = strings.TrimSpace(payload)
	if strings.HasPrefix(payload, "#") {
		r, g, b, ok := parseHexColor(payload)
		return terminalRGB{int(r), int(g), int(b)}, ok
	}
	for _, prefix := range []string{"rgb:", "rgba:"} {
		if strings.HasPrefix(payload, prefix) {
			return parseOSCColorTriplet(strings.TrimPrefix(payload, prefix))
		}
	}
	return terminalRGB{}, false
}

func parseOSCColorTriplet(s string) (terminalRGB, bool) {
	parts := strings.Split(s, "/")
	if len(parts) < 3 {
		return terminalRGB{}, false
	}
	r, okR := parseOSCColorComponent(parts[0])
	g, okG := parseOSCColorComponent(parts[1])
	b, okB := parseOSCColorComponent(parts[2])
	return terminalRGB{r, g, b}, okR && okG && okB
}

func parseOSCColorComponent(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 4 {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 16, 64)
	if err != nil {
		return 0, false
	}
	max := int64(1)<<(4*len(s)) - 1
	if max <= 0 {
		return 0, false
	}
	return int(v * 255 / max), true
}

func colorFGBGLooksLight() bool {
	parts := strings.Split(os.Getenv("COLORFGBG"), ";")
	if len(parts) == 0 {
		return false
	}
	bg, err := strconv.Atoi(parts[len(parts)-1])
	return err == nil && (bg == 7 || bg == 15)
}

func fgSGR(c cliColor) string {
	if supportsTrueColor() && c.hex != "" {
		r, g, b, ok := parseHexColor(c.hex)
		if ok {
			return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
		}
	}
	return fmt.Sprintf("\033[38;5;%dm", c.xterm)
}

func bgSGR(c cliColor) string {
	if supportsTrueColor() && c.hex != "" {
		r, g, b, ok := parseHexColor(c.hex)
		if ok {
			return fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b)
		}
	}
	return fmt.Sprintf("\033[48;5;%dm", c.xterm)
}

func parseHexColor(hex string) (int64, int64, int64, bool) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	r, errR := strconv.ParseInt(hex[0:2], 16, 64)
	g, errG := strconv.ParseInt(hex[2:4], 16, 64)
	b, errB := strconv.ParseInt(hex[4:6], 16, 64)
	return r, g, b, errR == nil && errG == nil && errB == nil
}

func supportsTrueColor() bool {
	ct := strings.ToLower(os.Getenv("COLORTERM"))
	if strings.Contains(ct, "truecolor") || strings.Contains(ct, "24bit") {
		return true
	}
	if os.Getenv("WT_SESSION") != "" { // Windows Terminal — full 24-bit since 2019
		return true
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "vscode", "WarpTerminal", "ghostty", "rio":
		return true
	default:
		return false
	}
}

func themeFg(c cliColor, s string) string {
	return sgr(fgSGR(c), s)
}

func themeLipColor(c cliColor) color.Color {
	if supportsTrueColor() && c.hex != "" {
		return lipgloss.Color(c.hex)
	}
	return lipgloss.Color(strconv.Itoa(c.xterm))
}

func themeStyle(c cliColor) lipgloss.Style {
	if !colorEnabled {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(themeLipColor(c))
}

func themeBGStyle(c cliColor) lipgloss.Style {
	if !colorEnabled {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Background(themeLipColor(c))
}

func withThemeFG(st lipgloss.Style, c cliColor) lipgloss.Style {
	if !colorEnabled {
		return st
	}
	return st.Foreground(themeLipColor(c))
}

func withThemeBorderFG(st lipgloss.Style, c cliColor) lipgloss.Style {
	if !colorEnabled {
		return st
	}
	return st.BorderForeground(themeLipColor(c))
}

func init() {
	refreshCLIStyles()
}

func refreshCLIStyles() {
	inputBoxStyle = withThemeBorderFG(lipgloss.NewStyle().
		Border(lipgloss.ThickBorder(), true, false, true, false), activeCLITheme.border).
		PaddingLeft(1)
	// Pinned panels are terminal-native overlays: no floating-card border and no
	// background fill. Their hierarchy comes from spacing, row markers, and text
	// colour so they stay close to Amp's compact TUI feel.
	approvalBannerStyle = lipgloss.NewStyle().PaddingLeft(1)
	todoPanelStyle = lipgloss.NewStyle().PaddingLeft(1)
	if colorEnabled {
		// The status block is rendered at the page's ambient `ink` colour, not
		// on a `surface` panel. Painting it with `surface` here made the
		// unused right-hand cells read as a near-black band — in the dark
		// theme `surface` (#1b1410, luma 234) and `ink` (#15100d, luma 233)
		// are one step apart, so the band looked identical to a "no fill"
		// background and visually it read as a black strip behind the text.
		// Leaving Background unset lets those cells fall through to the
		// page-level ink paint, which is the same colour the rest of the
		// transcript already lives on, so the seam disappears.
		statusBlockStyle = lipgloss.NewStyle().
			Foreground(themeLipColor(activeCLITheme.faint))
		workingStyle = lipgloss.NewStyle().
			Foreground(themeLipColor(activeCLITheme.text)).
			Bold(true)
	} else {
		statusBlockStyle = lipgloss.NewStyle()
		workingStyle = lipgloss.NewStyle().Bold(true)
	}
	compSelStyle = themeStyle(activeCLITheme.accent).Bold(true)
	choicePanelStyle = lipgloss.NewStyle().PaddingLeft(1)
	scrollThumbStyle = themeStyle(activeCLITheme.faint)
	scrollTrackStyle = themeStyle(activeCLITheme.border)
}

func applyTextareaTheme(ti *textarea.Model) {
	plain := lipgloss.NewStyle()
	weak := themeStyle(activeCLITheme.faint)
	if !colorEnabled {
		weak = plain
	}

	styles := ti.Styles()
	styles.Focused = textarea.StyleState{
		Base:             plain,
		Text:             plain,
		CursorLine:       plain,
		CursorLineNumber: weak,
		EndOfBuffer:      weak,
		LineNumber:       weak,
		Placeholder:      weak,
		Prompt:           weak,
	}
	styles.Blurred = textarea.StyleState{
		Base:             plain,
		Text:             plain,
		CursorLine:       plain,
		CursorLineNumber: weak,
		EndOfBuffer:      weak,
		LineNumber:       weak,
		Placeholder:      weak,
		Prompt:           weak,
	}
	if colorEnabled {
		styles.Cursor.Color = themeLipColor(activeCLITheme.accent)
	} else {
		styles.Cursor.Color = nil
	}
	ti.SetStyles(styles)
}

func (m *chatTUI) runThemeSubcommand(input string) {
	args := tokenizeArgs(input)
	if len(args) < 2 {
		m.notice(i18n.M.ThemeHeader + "\n" + describeCLIThemes() + "\n" + i18n.M.ThemeHint)
		return
	}
	name := strings.ToLower(args[1])
	var theme cliPalette
	switch name {
	case "auto", "light", "dark":
		theme = setCLIThemeMode(name)
	default:
		next, ok := setCLIThemeStyle(name)
		if !ok {
			m.notice(fmt.Sprintf(i18n.M.ThemeUnknownFmt, name) + "\n" + describeCLIThemes())
			return
		}
		theme = next
	}
	m.refreshRuntimeTheme()
	m.notice(fmt.Sprintf(i18n.M.ThemeChangedFmt, theme.name, theme.style))
}

func (m *chatTUI) refreshRuntimeTheme() {
	m.spinner.Style = themeStyle(activeCLITheme.accent)
	applyTextareaTheme(&m.input)
}

func describeCLIThemes() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  auto · light · dark\n", dim("modes:"))
	for _, st := range cliThemeStyles {
		marker := "  "
		if st.name == activeCLITheme.style {
			marker = accent("› ")
		}
		fmt.Fprintf(&b, "%s%-10s %s  %s\n", marker, st.name, dim(st.mode), dim(st.description))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *chatTUI) themeArgItems(val string) ([]compItem, int, bool) {
	cmdEnd := strings.IndexAny(val, " \t")
	if cmdEnd < 0 || val[:cmdEnd] != "/theme" {
		return nil, 0, false
	}
	from := strings.LastIndexAny(val, " \t") + 1
	prior := strings.Fields(val[:from])
	if len(prior) != 1 {
		return nil, from, true
	}
	cur := strings.ToLower(val[from:])
	items := []struct {
		label string
		mode  string
		desc  string
	}{
		{label: "auto", mode: "mode", desc: "detect terminal background"},
		{label: "light", mode: "mode", desc: "force light shell"},
		{label: "dark", mode: "mode", desc: "force dark shell"},
	}
	var out []compItem
	for _, it := range items {
		if cur != "" && !strings.HasPrefix(it.label, cur) {
			continue
		}
		out = append(out, compItem{label: it.label, insert: it.label, hint: it.mode + " · " + it.desc})
	}
	for _, st := range cliThemeStyles {
		if cur != "" && !strings.HasPrefix(st.name, cur) {
			continue
		}
		hint := st.mode + " · " + st.description
		if st.name == activeCLITheme.style {
			hint = i18n.M.ArgThemeCurrent
		}
		out = append(out, compItem{label: st.name, insert: st.name, hint: hint})
	}
	return out, from, true
}
