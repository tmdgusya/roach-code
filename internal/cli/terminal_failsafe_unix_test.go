//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || aix || zos

package cli

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type fakeTeaProgram struct {
	killCount int
}

func (f *fakeTeaProgram) Run() (tea.Model, error) { return nil, tea.ErrProgramKilled }
func (f *fakeTeaProgram) Kill()                   { f.killCount++ }

func TestTerminalCleanupSequenceDisablesApplicationModes(t *testing.T) {
	seq := terminalCleanupSequence()
	for _, want := range []string{
		"\x1b[?1000l",
		"\x1b[?1002l",
		"\x1b[?1003l",
		"\x1b[?1006l",
		"\x1b[?2004l",
		"\x1b[?1049l",
		"\x1b[?25h",
	} {
		if !strings.Contains(seq, want) {
			t.Fatalf("cleanup sequence missing %q in %q", want, seq)
		}
	}
}

// TestTerminalFailsafeSessionEndersKill verifies that session-ending signals
// (SIGHUP, SIGQUIT, SIGTERM) still tear the TUI down with terminal cleanup.
// These are the signals that should abort the program.
func TestTerminalFailsafeSessionEndersKill(t *testing.T) {
	for _, sig := range []os.Signal{syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM} {
		var out bytes.Buffer
		prog := &fakeTeaProgram{}
		g := &terminalFailsafe{program: prog, out: &out}

		g.handle(sig)

		if prog.killCount != 1 {
			t.Fatalf("%v should kill the TUI once, got %d", sig, prog.killCount)
		}
		if !errors.Is(g.cause(), errTerminalTerminated) {
			t.Fatalf("%v cause = %v, want errTerminalTerminated", sig, g.cause())
		}
		if !strings.Contains(out.String(), "\x1b[?1002l") || !strings.Contains(out.String(), "\x1b[?1049l") {
			t.Fatalf("%v cleanup output did not disable mouse and alt-screen: %q", sig, out.String())
		}
	}
}

// TestJobControlSignalsAreIgnored is the regression test for the bug where
// roach-code self-terminated when backgrounded. SIGTTIN/SIGTTOU must be ignored
// (not delivered to the failsafe, not causing a kill) so a session survives being
// put in the background — matching Claude Code / Codex behavior. We assert that
// the failsafe's signal list excludes them and that ignoreJobControlSignals is
// safe to call (idempotent, doesn't panic, doesn't block).
func TestJobControlSignalsAreIgnored(t *testing.T) {
	// Signal list must NOT contain job-control signals — they're handled by
	// signal.Ignore instead. If someone re-adds them here, backgrounded
	// roach-code will die again.
	for _, sig := range terminalFailsafeSignals() {
		if sig == syscall.SIGTTIN || sig == syscall.SIGTTOU {
			t.Fatalf("terminalFailsafeSignals must not include job-control signal %v — "+
				"backgrounded roach-code would self-terminate again", sig)
		}
	}

	// ignoreJobControlSignals must be safe to call. It's idempotent.
	ignoreJobControlSignals()
	ignoreJobControlSignals()

	// After ignoring, sending SIGTTIN/SIGTTOU to ourselves must NOT be fatal:
	// the process is still alive after delivery. Use a deadline so a regression
	// (default disposition kills the process) surfaces as a test failure rather
	// than a hang.
	done := make(chan struct{})
	go func() {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTTOU)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTTIN)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SIGTTOU/SIGTTIN delivery blocked or killed the process — ignore failed")
	}
}
