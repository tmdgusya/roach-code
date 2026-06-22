package cli

import (
	"reflect"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"roach-code/internal/textarea"
)

func TestConfigureCLIThemeSwitchesModeAndDefaultStyle(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("ROACH_THEME", "")
	t.Setenv("ROACH_THEME_STYLE", "")
	defer restoreThemeForTest(colorEnabled, activeCLITheme)
	colorEnabled = true

	configureCLITheme("light")
	if activeCLITheme.name != "light" || activeCLITheme.style != "amp" {
		t.Fatalf("light theme = %s/%s, want light/amp", activeCLITheme.name, activeCLITheme.style)
	}
	if got := accent("x"); !strings.HasPrefix(got, "\033[38;5;74m") {
		t.Fatalf("light default accent = %q, want amp xterm 74", got)
	}

	configureCLITheme("dark")
	if activeCLITheme.name != "dark" || activeCLITheme.style != "amp" {
		t.Fatalf("dark theme = %s/%s, want dark/amp", activeCLITheme.name, activeCLITheme.style)
	}
	if got := accent("x"); !strings.HasPrefix(got, ansiAccent) {
		t.Fatalf("dark accent = %q, want %q", got, ansiAccent)
	}
}

func TestDetectColorHonorsForceColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	t.Setenv("FORCE_COLOR", "1")
	if !detectColor() {
		t.Fatal("FORCE_COLOR should enable colour even when TERM=dumb")
	}

	t.Setenv("NO_COLOR", "1")
	if detectColor() {
		t.Fatal("NO_COLOR must override FORCE_COLOR")
	}
}

func TestConfigureCLIThemeStyleOverride(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("ROACH_THEME", "")
	t.Setenv("ROACH_THEME_STYLE", "")
	defer restoreThemeForTest(colorEnabled, activeCLITheme)
	colorEnabled = true

	configureCLIThemeWithStyle("dark", "amp")
	if activeCLITheme.name != "dark" || activeCLITheme.style != "amp" {
		t.Fatalf("theme = %s/%s, want dark/amp", activeCLITheme.name, activeCLITheme.style)
	}
	if got := accent("x"); !strings.HasPrefix(got, "\033[38;5;65m") {
		t.Fatalf("amp accent = %q, want xterm 65", got)
	}

	configureCLITheme("amp")
	if activeCLITheme.name != "dark" || activeCLITheme.style != "amp" {
		t.Fatalf("theme style command resolved %s/%s, want dark/amp", activeCLITheme.name, activeCLITheme.style)
	}
}

func TestConfigureCLIThemeHonorsEnvOverride(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("ROACH_THEME", "amp")
	t.Setenv("ROACH_THEME_STYLE", "")
	defer restoreThemeForTest(colorEnabled, activeCLITheme)
	colorEnabled = true

	configureCLIThemeWithStyle("light", "glacier")
	if activeCLITheme.name != "dark" || activeCLITheme.style != "amp" {
		t.Fatalf("ROACH_THEME override resolved %s/%s, want dark/amp", activeCLITheme.name, activeCLITheme.style)
	}
}

func TestThemeArgCompletion(t *testing.T) {
	defer restoreThemeForTest(colorEnabled, activeCLITheme)
	colorEnabled = true
	configureCLIThemeWithStyle("dark", "amp")

	m := newTestChatTUI()
	items, _, ok := m.slashArgItems("/theme ")
	if !ok || len(items) == 0 {
		t.Fatalf("/theme arg completion should offer themes, ok=%v n=%d", ok, len(items))
	}
	if !hasLabel(items, "auto") || !hasLabel(items, "amp") || !hasLabel(items, "light") {
		t.Fatalf("/theme completion missing expected themes: %v", labels(items))
	}
}

func TestRunThemeSubcommandSwitchesAccentAndTextarea(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("ROACH_THEME", "")
	t.Setenv("ROACH_THEME_STYLE", "")
	defer restoreThemeForTest(colorEnabled, activeCLITheme)
	colorEnabled = true
	configureCLIThemeWithStyle("dark", "amp")

	m := newTestChatTUI()
	m.runThemeSubcommand("/theme amp")
	if activeCLITheme.name != "dark" || activeCLITheme.style != "amp" {
		t.Fatalf("current theme = %s/%s, want dark/amp", activeCLITheme.name, activeCLITheme.style)
	}
	if got := accent("x"); !strings.HasPrefix(got, "\033[38;5;65m") {
		t.Fatalf("accent = %q, want amp xterm color", got)
	}
	if m.input.Styles().Cursor.Color == nil {
		t.Fatal("textarea cursor color was not refreshed")
	}
}

func TestAmpDarkThemeColorTokens(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("ROACH_THEME", "")
	t.Setenv("ROACH_THEME_STYLE", "")
	defer restoreThemeForTest(colorEnabled, activeCLITheme)
	colorEnabled = true

	configureCLIThemeWithStyle("dark", "amp")
	want := map[string]cliColor{
		"ink":       {hex: "#000000", xterm: 16},
		"accent":    {hex: "#48a36d", xterm: 65},
		"selection": {hex: "#48a36d", xterm: 65},
		"muted":     {hex: "#8a8a8a", xterm: 245},
		"faint":     {hex: "#5f5f5f", xterm: 240},
		"border":    {hex: "#1a1a1a", xterm: 234},
		"surface":   {hex: "#080808", xterm: 232},
		"toolRead":  {hex: "#8a8a8a", xterm: 245},
		"toolProc":  {hex: "#6f6f6f", xterm: 242},
	}
	got := map[string]cliColor{
		"ink":       activeCLITheme.ink,
		"accent":    activeCLITheme.accent,
		"selection": activeCLITheme.selection,
		"muted":     activeCLITheme.muted,
		"faint":     activeCLITheme.faint,
		"border":    activeCLITheme.border,
		"surface":   activeCLITheme.surface,
		"toolRead":  activeCLITheme.toolRead,
		"toolProc":  activeCLITheme.toolProc,
	}
	for name, wantColor := range want {
		if got[name] != wantColor {
			t.Fatalf("%s = %+v, want %+v", name, got[name], wantColor)
		}
	}
	if activeCLITheme.accent.hex == "#6b8cce" {
		t.Fatal("dark amp accent must not be the previous blue #6b8cce")
	}
	if !reflect.DeepEqual(inputBoxStyle.GetBorderTopForeground(), themeLipColor(activeCLITheme.border)) {
		t.Fatalf("input border should use border token, got %v want %v", inputBoxStyle.GetBorderTopForeground(), themeLipColor(activeCLITheme.border))
	}
	if reflect.DeepEqual(inputBoxStyle.GetBorderTopForeground(), themeLipColor(activeCLITheme.accent)) {
		t.Fatal("input border must not use accent token")
	}
	if !reflect.DeepEqual(scrollThumbStyle.GetForeground(), themeLipColor(activeCLITheme.faint)) {
		t.Fatalf("scroll thumb should use faint token, got %v want %v", scrollThumbStyle.GetForeground(), themeLipColor(activeCLITheme.faint))
	}
}

func TestParseOSC11Response(t *testing.T) {
	for _, tt := range []struct {
		name  string
		in    string
		want  terminalRGB
		light bool
	}{
		{
			name:  "black-rgb",
			in:    "\x1b]11;rgb:0000/0000/0000\a",
			want:  terminalRGB{0, 0, 0},
			light: false,
		},
		{
			name:  "white-rgb",
			in:    "\x1b]11;rgb:ffff/ffff/ffff\x1b\\",
			want:  terminalRGB{255, 255, 255},
			light: true,
		},
		{
			name:  "hex",
			in:    "\x1b]11;#f8f8f8\a",
			want:  terminalRGB{248, 248, 248},
			light: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseOSC11Response(tt.in)
			if !ok {
				t.Fatalf("parseOSC11Response returned !ok")
			}
			if got != tt.want {
				t.Fatalf("rgb = %+v, want %+v", got, tt.want)
			}
			if got.looksLight() != tt.light {
				t.Fatalf("looksLight = %v, want %v", got.looksLight(), tt.light)
			}
		})
	}
}

func TestAutoThemeFallsBackToColorFGBG(t *testing.T) {
	defer restoreThemeForTest(colorEnabled, activeCLITheme)
	colorEnabled = false

	t.Setenv("COLORFGBG", "0;15")
	if got := resolveCLITheme("auto").name; got != "light" {
		t.Fatalf("COLORFGBG light fallback resolved %q, want light", got)
	}

	t.Setenv("COLORFGBG", "15;0")
	if got := resolveCLITheme("auto").name; got != "dark" {
		t.Fatalf("COLORFGBG dark fallback resolved %q, want dark", got)
	}
}

func TestApplyTextareaThemeClearsCursorLineBackground(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("ROACH_THEME", "")
	t.Setenv("ROACH_THEME_STYLE", "")
	defer restoreThemeForTest(colorEnabled, activeCLITheme)
	colorEnabled = true

	for _, mode := range []string{"dark", "light", "auto"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "auto" {
				t.Setenv("COLORFGBG", "0;15")
			} else {
				t.Setenv("COLORFGBG", "")
			}
			configureCLITheme(mode)

			ti := textarea.New()
			applyTextareaTheme(&ti)
			styles := ti.Styles()
			emptyBG := lipgloss.NewStyle().GetBackground()

			if bg := styles.Focused.CursorLine.GetBackground(); !reflect.DeepEqual(bg, emptyBG) {
				t.Fatalf("focused cursor line background = %v, want empty", bg)
			}
			if bg := styles.Blurred.CursorLine.GetBackground(); !reflect.DeepEqual(bg, emptyBG) {
				t.Fatalf("blurred cursor line background = %v, want empty", bg)
			}
			if bg := styles.Focused.EndOfBuffer.GetBackground(); !reflect.DeepEqual(bg, emptyBG) {
				t.Fatalf("end-of-buffer background = %v, want empty", bg)
			}
			if styles.Cursor.Color == nil {
				t.Fatal("cursor color is nil with color enabled")
			}
		})
	}
}

// TestRuntimeAutoThemeDoesNotProbeStdin guards the fix for a runtime `/theme auto`
// that live-probed the terminal (raw-mode stdin read) while the TUI owned stdin,
// racing bubbletea's input reader. The switch must resolve via the COLORFGBG
// fallback instead, never invoking the probe.
func TestRuntimeAutoThemeDoesNotProbeStdin(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("ROACH_THEME", "")
	t.Setenv("ROACH_THEME_STYLE", "")
	t.Setenv("COLORFGBG", "15;0")
	defer restoreThemeForTest(colorEnabled, activeCLITheme)
	colorEnabled = true

	prev := queryTerminalBackgroundForTheme
	defer func() { queryTerminalBackgroundForTheme = prev }()
	probed := false
	queryTerminalBackgroundForTheme = func() (terminalRGB, bool) {
		probed = true
		return terminalRGB{}, false
	}

	if got := setCLIThemeMode("auto").name; got != "dark" {
		t.Fatalf("auto with COLORFGBG=15;0 resolved %q, want dark", got)
	}
	if probed {
		t.Fatal("runtime /theme auto probed the terminal while the TUI owns stdin")
	}
}

func restoreThemeForTest(prevColor bool, prevTheme cliPalette) {
	colorEnabled = prevColor
	activeCLITheme = prevTheme
	refreshCLIStyles()
}
