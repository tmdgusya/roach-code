package cli

import (
	"errors"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestScrollbarThumb(t *testing.T) {
	if _, size := scrollbarThumb(10, 0, 5); size != 0 {
		t.Errorf("content within viewport should have no thumb, got size %d", size)
	}
	if start, _ := scrollbarThumb(10, 0, 100); start != 0 {
		t.Errorf("at top the thumb starts at row 0, got %d", start)
	}
	const h, total = 10, 100
	if start, size := scrollbarThumb(h, total-h, total); start+size != h {
		t.Errorf("at bottom the thumb reaches the last row: start=%d size=%d h=%d", start, size, h)
	}
}

func TestEdgeScrollDir(t *testing.T) {
	const h = 10
	if got := edgeScrollDir(0, h); got != -1 {
		t.Errorf("top edge dir = %d, want -1", got)
	}
	if got := edgeScrollDir(h-1, h); got != 1 {
		t.Errorf("bottom edge dir = %d, want 1", got)
	}
	if got := edgeScrollDir(h/2, h); got != 0 {
		t.Errorf("middle dir = %d, want 0", got)
	}
}

func TestSelSpan(t *testing.T) {
	start, end, cw := selPos{line: 1, col: 3}, selPos{line: 3, col: 5}, 20
	for _, tc := range []struct {
		idx         int
		wantOK      bool
		wantLo, wHi int
	}{
		{0, false, 0, 0}, // above
		{1, true, 3, cw}, // first line: anchor col → right edge
		{2, true, 0, cw}, // middle line: full width
		{3, true, 0, 5},  // last line: left edge → head col
		{4, false, 0, 0}, // below
	} {
		lo, hi, ok := selSpan(tc.idx, start, end, cw)
		if ok != tc.wantOK || (ok && (lo != tc.wantLo || hi != tc.wHi)) {
			t.Errorf("selSpan(%d) = (%d,%d,%v), want (%d,%d,%v)", tc.idx, lo, hi, ok, tc.wantLo, tc.wHi, tc.wantOK)
		}
	}
}

func TestSelectedTextMultiLine(t *testing.T) {
	m := newTestChatTUI()
	m.wrappedLines = []string{"hello world", "second line", "third row"}
	m.sel = selection{active: true, anchor: selPos{line: 0, col: 6}, head: selPos{line: 2, col: 5}}

	if got, want := m.selectedText(), "world\nsecond line\nthird"; got != want {
		t.Errorf("selectedText() = %q, want %q", got, want)
	}

	// A zero-width selection (plain click) copies nothing.
	m.sel = selection{active: true, anchor: selPos{line: 0, col: 3}, head: selPos{line: 0, col: 3}}
	if got := m.selectedText(); got != "" {
		t.Errorf("empty selection should yield no text, got %q", got)
	}
}

// drainCmd executes a command and any commands it batches, so a test can drive
// the clipboard side effects of a tea.Batch(copy, finalize) return.
func drainCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			drainCmd(c)
		}
	}
}

// TestDragReleaseAutoCopies locks in the "just drag to copy" convenience: a
// release after a real drag-selection copies the selection to the clipboard AND
// drops the highlight, while a plain click (empty selection) copies nothing and
// clears too.
func TestDragReleaseAutoCopies(t *testing.T) {
	defer func(fn func(string) error) { clipboardWriteAll = fn }(clipboardWriteAll)
	var got string
	clipboardWriteAll = func(s string) error { got = s; return nil }

	m := newTestChatTUI()
	m.wrappedLines = []string{"hello world", "second line"}

	// A real drag-selection (anchor != head) auto-copies on release and clears.
	m.sel = selection{active: true, anchor: selPos{line: 0, col: 0}, head: selPos{line: 0, col: 5}}
	rel, cmd := m.update(tea.MouseReleaseMsg{Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("left-release with a non-empty selection must return a copy command")
	}
	drainCmd(cmd)
	if got != "hello" {
		t.Errorf("drag-release copied %q, want %q", got, "hello")
	}
	if rel.(chatTUI).sel.active {
		t.Error("drag-release should drop the highlight once the drag ends")
	}

	// Robustness: a release that reports a different button (terminals vary in SGR
	// mode) must still copy an active drag-selection — the selection itself, not the
	// release button, is the signal.
	got = ""
	m.sel = selection{active: true, anchor: selPos{line: 1, col: 0}, head: selPos{line: 1, col: 6}}
	_, cmd = m.update(tea.MouseReleaseMsg{Button: tea.MouseRight})
	drainCmd(cmd)
	if got != "second" {
		t.Errorf("release with a non-left button should still copy the drag-selection, got %q", got)
	}

	// A plain click (anchor == head) copies nothing and clears the selection.
	got = ""
	m.sel = selection{active: true, anchor: selPos{line: 0, col: 2}, head: selPos{line: 0, col: 2}}
	next, _ := m.update(tea.MouseReleaseMsg{Button: tea.MouseLeft})
	if got != "" {
		t.Errorf("plain click should copy nothing, got %q", got)
	}
	if next.(chatTUI).sel.active {
		t.Error("plain click should clear the selection")
	}
}

func TestCopyToClipboardPlatformSuccess(t *testing.T) {
	defer func(fn func(string) error) { clipboardWriteAll = fn }(clipboardWriteAll)
	var got string
	clipboardWriteAll = func(s string) error { got = s; return nil }

	if msg := copyToClipboard("hello")(); msg != nil {
		t.Errorf("platform write succeeded; want nil msg (no OSC 52 fallback), got %#v", msg)
	}
	if got != "hello" {
		t.Errorf("clipboardWriteAll got %q, want %q", got, "hello")
	}
}

func TestCopyToClipboardOSC52Fallback(t *testing.T) {
	defer func(fn func(string) error) { clipboardWriteAll = fn }(clipboardWriteAll)
	clipboardWriteAll = func(string) error { return errors.New("no display (tmux/ssh)") }

	// On failure the command must return the *message* tea.SetClipboard yields —
	// the runtime handles it by emitting OSC 52 (bubbletea tea.go: setClipboardMsg
	// -> ansi.SetSystemClipboard). Returning the command itself would be dropped.
	got := copyToClipboard("copied text")()
	want := tea.SetClipboard("copied text")()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback msg = %#v (%T), want the runtime-handled setClipboardMsg %#v (%T)", got, got, want, want)
	}
}
