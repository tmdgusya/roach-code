package cli

import (
	"fmt"
	"image/color"
	"os"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"

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
	userBubbleBG cliColor
	diffAddBG    cliColor
	diffDelBG    cliColor
	toolRead     cliColor
	toolProc     cliColor
	surface      cliColor // warm panel fill behind tool/output blocks (ambient depth)
	surfaceLift  cliColor // the panel's top row — one step toward the light (top-left lamp)
	surfaceSeam  cliColor // 1-cell lit edge at col 3, where the lamp catches the structural spine
	text         cliColor // base body-text foreground (warm off-white, not terminal default)
	ink          cliColor // page background painted across the whole alt-screen (the ambient canvas)
}

type cliThemeStyle struct {
	name        string
	mode        string
	accent      cliColor
	description string
}

var (
	// The default dark shell is "warm coal": a near-black backdrop tinted brown
	// rather than neutral graphite, low-contrast warm off-white text, a copper-coral
	// glow accent, and seafoam/kelp as the cool counterpoint — an ambient, lamplit
	// register (inspired by gajae-code's red-claw) instead of a high-contrast neon
	// terminal. The glitch style still overrides these for the loud cyber look.
	cliDarkTheme = cliPalette{
		name:         "dark",
		style:        "graphite",
		accent:       cliColor{"#e0875c", 173}, // copper-coral glow
		muted:        cliColor{"#c6ad9d", 250}, // warm dusty taupe (primary text-ish)
		faint:        cliColor{"#8c7669", 243}, // muted coffee taupe
		success:      cliColor{"#7fd6a8", 114}, // seafoam kelp
		warn:         cliColor{"#e3aa5a", 179}, // warm amber
		err:          cliColor{"#e58a6f", 209}, // warm coral
		danger:       cliColor{"#e5565a", 203}, // alarm coral-red
		border:       cliColor{"#3a2a22", 236}, // warm coal-brown border
		selection:    cliColor{"#e0875c", 173},
		userBubbleBG: cliColor{"#2c1d15", 236}, // warm panel — clearly lifted, brighter than the tool surface
		diffAddBG:    cliColor{"#18291d", 22},  // warm-tinted dark green
		diffDelBG:    cliColor{"#321a17", 52},  // warm-tinted dark red
		toolRead:     cliColor{"#6fcabf", 79},  // seafoam (calmer than cyan)
		toolProc:     cliColor{"#c98fd0", 176}, // soft violet
		surface:      cliColor{"#1b1410", 234}, // tool-panel fill, dimmer than the user bubble
		surfaceLift:  cliColor{"#1e1712", 234}, // top row, +~4 luma toward the lamp (never +5 — that bands)
		surfaceSeam:  cliColor{"#241a13", 235}, // lit edge at col 3
		text:         cliColor{"#ece0d4", 253}, // warm off-white body text
		ink:          cliColor{"#15100d", 233}, // warm near-black page canvas (painted across the screen)
	}
	cliLightTheme = cliPalette{
		name:         "light",
		style:        "sandstone",
		accent:       cliColor{"#2f5fa8", 25},
		muted:        cliColor{"#555049", 239},
		faint:        cliColor{"#82796f", 243},
		success:      cliColor{"#5d9b66", 65},
		warn:         cliColor{"#b68120", 136},
		err:          cliColor{"#b94b4d", 131},
		danger:       cliColor{"#e5484d", 167},
		border:       cliColor{"#ded4c6", 252},
		selection:    cliColor{"#6f91d9", 68},
		userBubbleBG: cliColor{"#f5f0e8", 255},
		diffAddBG:    cliColor{"#e5f3e7", 254},
		diffDelBG:    cliColor{"#fae8e8", 255},
		toolRead:     cliColor{"#6f91d9", 68},
		toolProc:     cliColor{"#8a6bb8", 97},
		surface:      cliColor{"#f1ece2", 254}, // warm paper panel fill
		surfaceLift:  cliColor{"#f6f1e7", 255}, // top row, +~4 luma toward ink (brighter, not darker)
		surfaceSeam:  cliColor{"#f8f2e6", 255}, // lit edge at col 3
		text:         cliColor{"#2e2a24", 236}, // warm near-black body text
		ink:          cliColor{"#faf5ec", 255}, // warm paper page canvas
	}
	cliThemeStyles = []cliThemeStyle{
		{name: "graphite", mode: "dark", accent: cliColor{"#d97757", 173}, description: "warm clay accent"},
		{name: "ember", mode: "dark", accent: cliColor{"#f06d38", 209}, description: "hot orange accent"},
		{name: "aurora", mode: "dark", accent: cliColor{"#34c3a6", 79}, description: "cool teal accent"},
		{name: "midnight", mode: "dark", accent: cliColor{"#b18cff", 141}, description: "quiet violet accent"},
		{name: "glitch", mode: "dark", accent: cliColor{"#ff3df2", 201}, description: "neon magenta terminal"},
		{name: "sandstone", mode: "light", accent: cliColor{"#c2613f", 173}, description: "default warm light accent"},
		{name: "porcelain", mode: "light", accent: cliColor{"#7d63c8", 104}, description: "soft violet light accent"},
		{name: "linen", mode: "light", accent: cliColor{"#bd5d4d", 167}, description: "muted coral light accent"},
		{name: "glacier", mode: "light", accent: cliColor{"#357fa8", 74}, description: "cool blue light accent"},
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
	if style.name == "glitch" {
		base.muted = cliColor{"#b8fff7", 159}
		base.faint = cliColor{"#6f7f91", 245}
		base.border = cliColor{"#203345", 24}
		base.selection = cliColor{"#00e5ff", 45}
		base.userBubbleBG = cliColor{"#171527", 234}
		base.toolRead = cliColor{"#00e5ff", 45}
		base.toolProc = cliColor{"#ff3df2", 201}
		base.surface = cliColor{"#12101f", 233}
		base.surfaceLift = cliColor{"#16131f", 234}
		base.surfaceSeam = cliColor{"#1a1530", 235}
		base.text = cliColor{"#d7e9f2", 195} // cool glow text for the neon style
		base.ink = cliColor{"#0c0a16", 233}  // deep violet-black canvas
	}
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
	if mode == "light" {
		for _, st := range cliThemeStyles {
			if st.name == "sandstone" {
				return st
			}
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
		Border(lipgloss.ThickBorder(), true, false, true, false), activeCLITheme.accent).
		PaddingLeft(1)
	// The approval gate shares the chooser's thin (Normal) chassis; risk is carried
	// by the per-render border colour (amber for destructive, copper for benign,
	// set in frameApproval) and by the highlighted default row — not by a
	// permanently-loud thick amber frame that trains alarm-fatigue.
	approvalBannerStyle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), true, false, true, false).
		PaddingLeft(1)
	todoPanelStyle = withThemeBorderFG(lipgloss.NewStyle().
		Border(lipgloss.ThickBorder(), true, false, false, false), activeCLITheme.border).
		PaddingLeft(1)
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
	choicePanelStyle = withThemeBorderFG(lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), true, false, true, false), activeCLITheme.accent).
		PaddingLeft(1)
	scrollThumbStyle = themeStyle(activeCLITheme.accent)
	scrollTrackStyle = themeStyle(activeCLITheme.faint)
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
