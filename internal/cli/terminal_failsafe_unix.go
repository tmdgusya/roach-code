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
	guard := startTerminalFailsafe(p, captureTerminalStateSnapshot(), terminalControlWriter())
	defer guard.stop()
	defer forceRestoreTerminal(guard.snap, guard.out)

	model, err := p.Run()
	if cause := guard.cause(); cause != nil && errors.Is(err, tea.ErrProgramKilled) {
		return model, cause
	}
	return model, err
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
	return []os.Signal{
		syscall.SIGHUP,
		syscall.SIGQUIT,
		syscall.SIGTERM,
		syscall.SIGTTIN,
		syscall.SIGTTOU,
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
	switch sig {
	case syscall.SIGTTIN, syscall.SIGTTOU:
		g.setCause(errTerminalDetached)
		forceRestoreTerminal(g.snap, g.out)
		g.program.Kill()
	default:
		g.setCause(errTerminalTerminated)
		forceRestoreTerminal(g.snap, g.out)
		g.program.Kill()
	}
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
	if isTTY(os.Stdout) {
		return os.Stdout
	}
	if isTTY(os.Stderr) {
		return os.Stderr
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
