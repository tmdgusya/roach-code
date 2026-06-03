package cli

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"roach-code/internal/control"
	"roach-code/internal/event"
	"roach-code/internal/provider"
)

// TestBannerGlowAnimatesIn256 guards the real-world bug: on a 256-colour terminal the
// glow used to bail to a STATIC gradient (so it sat frozen while the thinking shimmer
// — which degrades gracefully via mixColor — kept animating). The crest must move
// across phases even without truecolor.
func TestBannerGlowAnimatesIn256(t *testing.T) {
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() { colorEnabled, activeCLITheme = prevColor, prevTheme })
	t.Setenv("COLORTERM", "")
	t.Setenv("WT_SESSION", "")
	t.Setenv("TERM_PROGRAM", "")
	colorEnabled = true
	setCLIThemeMode("dark")
	if supportsTrueColor() {
		t.Skip("env reports truecolor — can't exercise the 256 path here")
	}
	stops := []cliColor{activeCLITheme.accent, activeCLITheme.warn, activeCLITheme.toolRead}
	a := strings.Join(roachHeroShimmerRows(stops, 0), "\n")
	b := strings.Join(roachHeroShimmerRows(stops, 18), "\n")
	if a == b {
		t.Fatal("256-colour banner glow identical across phases — still bailing to static")
	}
}

type noopGlowRunner struct{}

func (noopGlowRunner) Run(_ context.Context, _ string) error                  { return nil }
func (noopGlowRunner) RunMessage(_ context.Context, _ provider.Message) error { return nil }

// glowTestEnv enables truecolor + a known theme for a banner test, restoring all
// global state on cleanup so it never pollutes the rest of the suite.
func glowTestEnv(t *testing.T) {
	t.Helper()
	prevColor, prevTheme := colorEnabled, activeCLITheme
	t.Cleanup(func() { colorEnabled, activeCLITheme = prevColor, prevTheme })
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("WT_SESSION", "1")
	colorEnabled = true
	setCLIThemeMode("dark")
}

// TestBannerGlowLiveAndFreezes proves the welcome glow engages as a LIVE View element:
// a fresh wide session sets bannerLive (no transcript entry), a frame advances the
// phase and reschedules, and the first turn freezes it + commits the static banner.
func TestBannerGlowLiveAndFreezes(t *testing.T) {
	glowTestEnv(t)

	m := newTestChatTUI()
	m.label = "glm-5.1"

	next, _ := m.update(tea.WindowSizeMsg{Width: 100, Height: 40})
	cm := next.(chatTUI)
	if !cm.bannerLive {
		t.Fatal("bannerLive should be true on a fresh wide session")
	}
	if len(cm.transcript) != 0 {
		t.Fatalf("live banner must NOT be committed to the transcript, got %d entries", len(cm.transcript))
	}

	// A frame advances the phase and reschedules (so the animation keeps running).
	next2, cmd := cm.update(bannerFrameMsg{})
	cm2 := next2.(chatTUI)
	if cm2.bannerPhase <= cm.bannerPhase {
		t.Fatal("bannerFrameMsg did not advance bannerPhase")
	}
	if cmd == nil {
		t.Fatal("bannerFrameMsg must reschedule the next frame while live")
	}

	// The first turn freezes it: bannerLive off, static banner committed to the transcript.
	ctrl := control.New(control.Options{Runner: noopGlowRunner{}, Sink: event.Discard, SessionDir: t.TempDir(), Label: "test"})
	cm2.ctrl = ctrl
	cm2.startTurnWithRaw("hi", "hi", "hi", "hi")
	if cm2.bannerLive {
		t.Fatal("the first turn must clear bannerLive")
	}
	if len(cm2.transcript) == 0 {
		t.Fatal("the static banner must be committed to the transcript on freeze")
	}
}

// TestBannerGlowRendersAcrossFrames drives the FULL Update→View flow and proves the
// rendered welcome banner actually changes between frames (the live element repaints).
func TestBannerGlowRendersAcrossFrames(t *testing.T) {
	glowTestEnv(t)
	m := newTestChatTUI()
	m.label = "glm-5.1"
	var model tea.Model = m

	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if !model.(chatTUI).bannerLive {
		t.Fatal("expected bannerLive after WindowSize")
	}
	r1 := model.(chatTUI).renderWelcomeBanner()
	if strings.TrimSpace(r1) == "" {
		t.Fatal("renderWelcomeBanner empty")
	}
	for i := 0; i < 6; i++ { // advance several frames of the crest
		model, _ = model.Update(bannerFrameMsg{})
	}
	r2 := model.(chatTUI).renderWelcomeBanner()
	if r1 == r2 {
		t.Fatal("welcome banner render identical across frames — not animating")
	}
}

// TestBannerGlowSkipsNarrow checks the glow stays off (static) where the hero art
// doesn't fit.
func TestBannerGlowSkipsNarrow(t *testing.T) {
	glowTestEnv(t)

	m := newTestChatTUI()
	next, _ := m.update(tea.WindowSizeMsg{Width: 20, Height: 40})
	cm := next.(chatTUI)
	if cm.bannerLive {
		t.Fatal("a terminal too narrow for the hero art must not go live")
	}
}
