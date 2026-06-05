package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestRenderTodoPanelNesting proves a level-1 sub-step renders indented under
// its level-0 phase in the pinned task panel.
func TestRenderTodoPanelNesting(t *testing.T) {
	m := newTestChatTUI()
	m.width = 60
	m.todoArgs = `{"todos":[` +
		`{"content":"Phase A","status":"in_progress","level":0},` +
		`{"content":"sub one","status":"pending","level":1}]}`

	out := ansi.Strip(m.renderTodoPanel())
	if !strings.Contains(out, "TASKS") || !strings.Contains(out, "[············]") {
		t.Fatalf("panel missing header progress bar:\n%s", out)
	}
	if !strings.Contains(out, "Phase A") {
		t.Fatalf("panel missing phase:\n%s", out)
	}
	if !strings.Contains(out, "     ╰─ ○ sub one") {
		t.Fatalf("sub-step not rendered on a closed child rail:\n%s", out)
	}
	assertPanelLinesFit(t, out, m.width)
}

func TestRenderTodoPanelNestedSiblingRails(t *testing.T) {
	m := newTestChatTUI()
	m.width = 72
	m.todoArgs = `{"todos":[` +
		`{"content":"Phase A","status":"in_progress","level":0},` +
		`{"content":"single child","status":"pending","level":1},` +
		`{"content":"Phase B","status":"pending","level":0},` +
		`{"content":"child B","status":"pending","level":1},` +
		`{"content":"grandchild B","status":"pending","level":2}]}`

	out := ansi.Strip(m.renderTodoPanel())
	if !strings.Contains(out, "  │  ╰─ ○ single child") {
		t.Fatalf("single child before next phase should close at its own level:\n%s", out)
	}
	if !strings.Contains(out, "     ╰─ ○ child B") || !strings.Contains(out, "        ╰─ ○ grandchild B") {
		t.Fatalf("closed ancestors should render spaces, not open rails:\n%s", out)
	}
	assertPanelLinesFit(t, out, m.width)
}

func TestRenderTodoPanelNestedOverflowAndNarrowWidth(t *testing.T) {
	m := newTestChatTUI()
	m.width = 38
	m.todoArgs = `{"todos":[` +
		`{"content":"phase","status":"in_progress","level":0},` +
		`{"content":"nested one with a long label","status":"pending","level":1},` +
		`{"content":"nested two with a long label","status":"pending","level":1},` +
		`{"content":"nested three with a long label","status":"pending","level":1},` +
		`{"content":"nested four with a long label","status":"pending","level":1},` +
		`{"content":"nested five with a long label","status":"pending","level":1},` +
		`{"content":"nested six with a long label","status":"pending","level":1},` +
		`{"content":"nested seven with a long label","status":"pending","level":1},` +
		`{"content":"nested eight with a long label","status":"pending","level":1}]}`

	out := ansi.Strip(m.renderTodoPanel())
	if !strings.Contains(out, "     ├─ ○ nested seven") || !strings.Contains(out, "     ╰─ +1 more") {
		t.Fatalf("nested overflow should keep the rail open before the footer:\n%s", out)
	}
	assertPanelLinesFit(t, out, m.width)
}
func TestRenderTodoPanelOverflowKeepsOpenRail(t *testing.T) {
	m := newTestChatTUI()
	m.width = 80
	m.todoArgs = `{"todos":[` +
		`{"content":"step 1","status":"completed"},` +
		`{"content":"step 2","status":"pending"},` +
		`{"content":"step 3","status":"pending"},` +
		`{"content":"step 4","status":"pending"},` +
		`{"content":"step 5","status":"pending"},` +
		`{"content":"step 6","status":"pending"},` +
		`{"content":"step 7","status":"pending"},` +
		`{"content":"step 8","status":"pending"},` +
		`{"content":"step 9","status":"pending"}]}`

	out := ansi.Strip(m.renderTodoPanel())
	if !strings.Contains(out, "[●···········]") {
		t.Fatalf("panel missing partial progress bar:\n%s", out)
	}
	if !strings.Contains(out, "  ├─ ○ step 8") || !strings.Contains(out, "  ╰─ +1 more") {
		t.Fatalf("overflow should keep the last shown item on an open rail before the footer:\n%s", out)
	}
	assertPanelLinesFit(t, out, m.width)
}

func assertPanelLinesFit(t *testing.T, panel string, width int) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(panel, "\n"), "\n") {
		if w := visibleWidth(line); w > width {
			t.Fatalf("panel line width %d > %d:\n%s", w, width, panel)
		}
	}
}
