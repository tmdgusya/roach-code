package cli

import (
	"fmt"
	"os"
	"strconv"
	"sync"

	tea "charm.land/bubbletea/v2"
)

// Input diagnostics. Terminals route Ctrl+V, drag, and bracketed paste very
// differently (Warp on Windows is especially quirky), so when image paste or
// drag-copy "doesn't work" the first question is always: what message did the
// program actually receive? Set ROACH_INPUT_DEBUG to a file path (or "1" for
// roach-input-debug.log in the cwd) and every inbound key/paste/mouse message is
// appended there. Unset => a no-op with zero overhead beyond one env lookup.
var (
	inputDebugOnce sync.Once
	inputDebugFile *os.File
)

func inputDebugLog(msg tea.Msg) {
	// Resolve the env exactly once: every message (incl. 60fps banner frames) runs
	// through here, so the steady state is a single atomic Once check + nil compare —
	// zero cost when ROACH_INPUT_DEBUG is unset.
	inputDebugOnce.Do(openInputDebug)
	if inputDebugFile == nil {
		return
	}
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		fmt.Fprintf(inputDebugFile, "KeyPress     %q\n", m.String())
	case tea.PasteMsg:
		fmt.Fprintf(inputDebugFile, "Paste        len=%d %s\n", len(m.Content), strconv.Quote(truncForLog(m.Content)))
	case tea.MouseClickMsg:
		fmt.Fprintf(inputDebugFile, "MouseClick   btn=%v x=%d y=%d\n", m.Button, m.X, m.Y)
	case tea.MouseReleaseMsg:
		fmt.Fprintf(inputDebugFile, "MouseRelease btn=%v x=%d y=%d\n", m.Button, m.X, m.Y)
	case tea.MouseWheelMsg:
		fmt.Fprintf(inputDebugFile, "MouseWheel   btn=%v\n", m.Button)
	default:
		fmt.Fprintf(inputDebugFile, "%T\n", msg)
	}
}

func openInputDebug() {
	path := os.Getenv("ROACH_INPUT_DEBUG")
	if path == "" {
		return
	}
	if path == "1" || path == "true" {
		path = "roach-input-debug.log"
	}
	inputDebugFile, _ = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

func truncForLog(s string) string {
	const max = 80
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
