package cli

import (
	"reflect"
	"testing"

	"roach-code/internal/agent"
)

// TestPanelRowCount locks the bottom-region row formula shared by bottomRows and
// View (via appendPanel): empty = 0 rows, otherwise newline count + 1 — so a
// panel ending in a newline counts the trailing empty line too.
func TestPanelRowCount(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty is zero rows", "", 0},
		{"single line", "abc", 1},
		{"two lines", "a\nb", 2},
		{"three lines", "a\nb\nc", 3},
		{"trailing newline counts the empty tail", "a\n", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := panelRowCount(c.in); got != c.want {
				t.Fatalf("panelRowCount(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// TestAppendPanel proves appendPanel reproduces the exact per-panel step View
// used to inline: skip empties, else append the panel and add panelRowCount(s)
// to the running row total — preserving order and accumulation.
func TestAppendPanel(t *testing.T) {
	t.Run("empty panel is a no-op", func(t *testing.T) {
		var parts []string
		rows := 0
		appendPanel(&parts, &rows, "")
		if len(parts) != 0 || rows != 0 {
			t.Fatalf("empty append changed state: parts=%v rows=%d", parts, rows)
		}
	})

	t.Run("single-line panel appends and counts one row", func(t *testing.T) {
		var parts []string
		rows := 0
		appendPanel(&parts, &rows, "hello")
		if !reflect.DeepEqual(parts, []string{"hello"}) || rows != 1 {
			t.Fatalf("got parts=%v rows=%d, want [hello] 1", parts, rows)
		}
	})

	t.Run("multi-line panel adds its full height", func(t *testing.T) {
		var parts []string
		rows := 0
		appendPanel(&parts, &rows, "x\ny\nz")
		if !reflect.DeepEqual(parts, []string{"x\ny\nz"}) || rows != 3 {
			t.Fatalf("got parts=%v rows=%d, want one 3-row panel", parts, rows)
		}
	})

	t.Run("accumulates in order and skips empties", func(t *testing.T) {
		var parts []string
		rows := 0
		appendPanel(&parts, &rows, "a")    // +1
		appendPanel(&parts, &rows, "")     // skipped, no row
		appendPanel(&parts, &rows, "b\nc") // +2
		if want := []string{"a", "b\nc"}; !reflect.DeepEqual(parts, want) {
			t.Fatalf("parts = %v, want %v", parts, want)
		}
		if rows != 3 {
			t.Fatalf("rows = %d, want 3", rows)
		}
	})
}

// TestBottomRowsCountsResumePicker is the regression guard for the layout fix:
// View pins the "/resume" picker above the input box, so bottomRows must include
// its rows too — otherwise the transcript viewport (sized as height - bottomRows)
// runs one panel too tall while the picker is open. It asserts that opening the
// picker grows bottomRows by EXACTLY the picker's rendered height; the closed
// case (a nil picker renders "" and adds zero) is covered by
// TestTranscriptViewportSizing's bottomRows==5 assertion.
func TestBottomRowsCountsResumePicker(t *testing.T) {
	m := newTestChatTUI()
	if m.resumePick != nil {
		t.Fatal("precondition: resume picker should start closed")
	}
	// All panels render "" here, so this is the no-picker baseline. bottomRows has
	// a value receiver and reads only struct fields, so the ONLY thing that differs
	// between this call and the one below is m.resumePick — the delta isolates it.
	base := m.bottomRows()

	m.resumePick = &resumePicker{
		sessions: []agent.SessionInfo{
			{Path: "/a.jsonl", Turns: 1, Preview: "first session"},
			{Path: "/b.jsonl", Turns: 2, Preview: "second session"},
		},
		sel:    0,
		active: -1,
	}
	panel := m.renderResumePicker()
	if panel == "" {
		t.Fatal("an open resume picker must render a non-empty panel")
	}
	want := panelRowCount(panel)
	if want < 2 {
		t.Fatalf("a picker with a title, 2 sessions and a hint should span several rows, got %d", want)
	}

	if delta := m.bottomRows() - base; delta != want {
		t.Fatalf("bottomRows grew by %d when /resume opened, want %d (the picker's rendered height); "+
			"View pins this panel, so the viewport budget must account for it", delta, want)
	}
}
