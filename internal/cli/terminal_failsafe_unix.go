//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || aix || zos

package cli

import (
	"errors"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"
)

var (
	errTerminalDetached   = errors.New("terminal detached while roach was reading input")
	errTerminalTerminated = errors.New("terminal session terminated")
)

type teaProgram interface {
	Run() (tea.Model, error)
	Kill()
}

type terminalStateSnapshot struct {
	inFD     int
	inState  *term.State
	outFD    int
	outState *term.State
}

type terminalFailsafe struct {
	program teaProgram
	snap    terminalStateSnapshot
	out     io.Writer

	sigCh  chan os.Signal
	stopCh chan struct{}
	doneCh chan struct{}

	stopOnce sync.Once
	causeMu  sync.Mutex
	causeErr error
}

func runTeaProgramWithTerminalFailsafe(p teaProgram) (tea.Model, error) {
	// Ignore job-control signals that fire when roach-code is backgrounded:
	// SIGTTIN (background read) and SIGTTOU (background write). These are
	// POSIX notifications, not fatal — Claude Code and Codex ignore them too,
	// letting the process keep running until the user foregrounds it again.
	// The previous design killed the TUI on these signals, which is why
	// roach-code died when left running in the background while other tools
	// survived. Ignoring here means a backgrounded roach-code just waits.
	ignoreJobControlSignals()

	guard := startTerminalFailsafe(p, captureTerminalStateSnapshot(), terminalControlWriter())
	defer guard.stop()
	defer forceRestoreTerminal(guard.snap, guard.out)

	model, err := p.Run()
	if cause := guard.cause(); cause != nil && errors.Is(err, tea.ErrProgramKilled) {
		return model, cause
	}
	return model, err
}

// ignoreJobControlSignals makes SIGTTIN and SIGTTOU no-ops for this process.
// Called once at TUI startup so a backgrounded session survives — the TUI is
// in raw mode + alt-screen and will simply resume when the user foregrounds
// the process. Safe to call multiple times; signal.Ignore is idempotent.
func ignoreJobControlSignals() {
	signal.Ignore(syscall.SIGTTIN, syscall.SIGTTOU)
}

func startTerminalFailsafe(p teaProgram, snap terminalStateSnapshot, out io.Writer) *terminalFailsafe {
	g := &terminalFailsafe{
		program: p,
		snap:    snap,
		out:     out,
		sigCh:   make(chan os.Signal, 1),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	signal.Notify(g.sigCh, terminalFailsafeSignals()...)
	go g.loop()
	return g
}

func terminalFailsafeSignals() []os.Signal {
	// SIGTTIN and SIGTTOU are deliberately excluded: they are ignored in
	// runTeaProgramWithTerminalFailsafe so a backgrounded roach-code survives
	// instead of self-terminating. Only session-ending signals reach this
	// list.
	return []os.Signal{
		syscall.SIGHUP,
		syscall.SIGQUIT,
		syscall.SIGTERM,
	}
}

func (g *terminalFailsafe) loop() {
	defer close(g.doneCh)
	defer signal.Stop(g.sigCh)

	for {
		select {
		case <-g.stopCh:
			return
		case sig := <-g.sigCh:
			g.handle(sig)
			return
		}
	}
}

func (g *terminalFailsafe) handle(sig os.Signal) {
	// SIGTTIN/SIGTTOU never reach here (ignored in runTeaProgramWithTerminalFailsafe);
	// every signal that does reach this point is a session-ender, so we clean up
	// the terminal and kill the TUI.
	g.setCause(errTerminalTerminated)
	forceRestoreTerminal(g.snap, g.out)
	g.program.Kill()
}

func (g *terminalFailsafe) setCause(err error) {
	g.causeMu.Lock()
	defer g.causeMu.Unlock()
	if g.causeErr == nil {
		g.causeErr = err
	}
}

func (g *terminalFailsafe) cause() error {
	g.causeMu.Lock()
	defer g.causeMu.Unlock()
	return g.causeErr
}

func (g *terminalFailsafe) stop() {
	g.stopOnce.Do(func() {
		close(g.stopCh)
		<-g.doneCh
	})
}

func captureTerminalStateSnapshot() terminalStateSnapshot {
	var snap terminalStateSnapshot
	if isTTY(os.Stdin) {
		fd := int(os.Stdin.Fd())
		if st, err := term.GetState(fd); err == nil {
			snap.inFD = fd
			snap.inState = st
		}
	}
	if isTTY(os.Stdout) {
		fd := int(os.Stdout.Fd())
		if st, err := term.GetState(fd); err == nil {
			snap.outFD = fd
			snap.outState = st
		}
	}
	return snap
}

func terminalControlWriter() io.Writer {
	// Prefer the terminal we're directly wired to. When roach-code is
	// backgrounded (stdin/stdout are no longer the controlling tty) the
	// cleanup sequence must still reach the controlling terminal so mouse
	// tracking, alt-screen, and other application modes are reliably
	// disabled — otherwise the user is left with a terminal that leaks
	// mouse-reporting escape sequences into the shell on every click.
	// Opening /dev/tty directly reaches the controlling terminal regardless
	// of redirection, which is exactly what we need for the failsafe path.
	if isTTY(os.Stdout) {
		return os.Stdout
	}
	if isTTY(os.Stderr) {
		return os.Stderr
	}
	if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		return f
	}
	return io.Discard
}

func forceRestoreTerminal(s terminalStateSnapshot, out io.Writer) {
	writeTerminalCleanupSequence(out)
	if s.inState != nil {
		_ = term.Restore(s.inFD, s.inState)
	}
	if s.outState != nil && s.outFD != s.inFD {
		_ = term.Restore(s.outFD, s.outState)
	}
}

func writeTerminalCleanupSequence(out io.Writer) {
	if out == nil {
		return
	}
	_, _ = io.WriteString(out, terminalCleanupSequence())
}

func terminalCleanupSequence() string {
	const resetModes = "\x1b[0m" +
		"\x1b[?1000l" + // normal mouse
		"\x1b[?1002l" + // cell-motion mouse
		"\x1b[?1003l" + // all-motion mouse
		"\x1b[?1005l" + // UTF-8 mouse
		"\x1b[?1006l" + // SGR mouse
		"\x1b[?1015l" + // urxvt mouse
		"\x1b[?1004l" + // focus tracking
		"\x1b[?2004l" + // bracketed paste
		"\x1b[>4m" + // modifyOtherKeys reset
		"\x1b[=0;1u" + // keyboard enhancement reset
		"\x1b[?25h" // cursor visible
	return resetModes + "\x1b[?1049l" + resetModes
}
