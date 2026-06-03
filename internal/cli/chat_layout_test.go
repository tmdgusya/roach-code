package cli

import (
	"reflect"
	"testing"
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
