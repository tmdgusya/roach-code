package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestUltragoalTask(t *testing.T) {
	cases := []struct {
		in       string
		wantTask string
		wantOK   bool
	}{
		{"ultragoal review every file", "review every file", true},
		{"ultragoal   trim me  ", "trim me", true},
		{"UltraGoal Mixed Case", "Mixed Case", true},  // keyword is case-insensitive
		{"/ultragoal slash form", "slash form", true}, // slash variant
		{"ultragoal", "", true},                       // keyword alone → trigger, empty task
		{"/ultragoal", "", true},
		{"ultragoaling is not it", "", false}, // needs a word boundary (space)
		{"please ultragoal later", "", false}, // only triggers as a prefix
		{"normal message", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		task, ok := ultragoalTask(c.in)
		if ok != c.wantOK || task != c.wantTask {
			t.Errorf("ultragoalTask(%q) = (%q, %v), want (%q, %v)", c.in, task, ok, c.wantTask, c.wantOK)
		}
	}
}

func TestUltragoalPreambleRequiresPlanningContract(t *testing.T) {
	required := []string{
		"calling the run_workflow tool exactly once",
		"plans first, investigates before acting",
		"plan object or array",
		"phases: investigation, execution, verification, synthesis",
		"work units",
		"dependencies",
		"investigation needs",
		"done criteria",
		"Use that plan to build the workflow graph",
		"Use parallel() only for genuinely independent units",
		"Verification levels:",
		"Research/analysis units",
		"Code-change units",
		"Security/input/file/network/auth changes",
		"If verification cannot be run",
		"decomposition plan used",
	}
	for _, want := range required {
		if !strings.Contains(ultragoalPreamble, want) {
			t.Fatalf("ultragoalPreamble missing %q", want)
		}
	}
}

// TestUltragoalPreviewShimmer covers the live preview: the moment the composer
// holds the keyword the banner glows and a self-rescheduling tick animates it,
// and the loop stops cleanly the instant the keyword is gone.
func TestUltragoalPreviewShimmer(t *testing.T) {
	m := newTestChatTUI()
	m.input.SetValue("ultragoal do the thing")
	if !m.ultragoalPreviewing() {
		t.Fatal("composer holding the keyword should be previewing")
	}
	if !m.ultragoalGlowing() {
		t.Fatal("input box should glow while previewing")
	}

	// A frame advances the phase and reschedules (keeps the shimmer animating).
	next, cmd := m.update(ultragoalFrameMsg{})
	cm := next.(chatTUI)
	if cm.ultragoalPhase <= m.ultragoalPhase {
		t.Fatal("ultragoalFrameMsg did not advance ultragoalPhase")
	}
	if cmd == nil {
		t.Fatal("ultragoalFrameMsg must reschedule the next frame while previewing")
	}
	if !cm.ultragoalTicking {
		t.Fatal("ticking must be true while the preview loop is alive")
	}

	// Clearing the keyword stops previewing and ends the loop (no reschedule).
	cm.input.SetValue("just a normal message")
	if cm.ultragoalPreviewing() {
		t.Fatal("non-keyword input must not preview")
	}
	next2, cmd2 := cm.update(ultragoalFrameMsg{})
	cm2 := next2.(chatTUI)
	if cmd2 != nil {
		t.Fatal("ultragoalFrameMsg must NOT reschedule once no longer previewing")
	}
	if cm2.ultragoalTicking {
		t.Fatal("ticking must be false once the preview loop stops")
	}
}

// TestShimmerInputBoxGlowsText checks the two invariants that make the text glow
// safe: every line keeps its visible width (so the cursor and the bottom-region
// height never drift — the latter is what pushed the statusline off-screen), and
// the text line is actually recoloured when colour is on.
func TestShimmerInputBoxGlowsText(t *testing.T) {
	defer func(prev bool) { colorEnabled = prev }(colorEnabled)
	colorEnabled = true

	box := "━━━━━━━━━━\n ultragoal review files   \n━━━━━━━━━━"
	got := shimmerInputBox(box, 4)

	in := strings.Split(box, "\n")
	out := strings.Split(got, "\n")
	if len(in) != len(out) {
		t.Fatalf("line count changed: %d -> %d", len(in), len(out))
	}
	for i := range in {
		if w0, w1 := visibleWidth(in[i]), visibleWidth(out[i]); w0 != w1 {
			t.Fatalf("line %d visible width changed %d -> %d (cursor/layout would drift)", i, w0, w1)
		}
	}
	// The typed-text line must carry colour (the glow) — the whole point.
	if !strings.Contains(out[1], "\x1b[") {
		t.Fatal("text line should be shimmered (carry colour) when colorEnabled")
	}
	// The visible glyphs must survive untouched (only colour is added).
	if ansi.Strip(out[1]) != in[1] {
		t.Fatalf("text changed under the colour: %q -> %q", in[1], ansi.Strip(out[1]))
	}
}
