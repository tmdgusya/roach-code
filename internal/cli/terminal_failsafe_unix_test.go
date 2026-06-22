//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || aix || zos

package cli

import (
	"bytes"
	"errors"
	"strings"
	"syscall"
	"testing"

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

func TestTerminalFailsafeDetachedSignalsKillInsteadOfStopping(t *testing.T) {
	var out bytes.Buffer
	prog := &fakeTeaProgram{}
	g := &terminalFailsafe{program: prog, out: &out}

	g.handle(syscall.SIGTTIN)

	if prog.killCount != 1 {
		t.Fatalf("SIGTTIN should kill the TUI once, got %d", prog.killCount)
	}
	if !errors.Is(g.cause(), errTerminalDetached) {
		t.Fatalf("cause = %v, want errTerminalDetached", g.cause())
	}
	if !strings.Contains(out.String(), "\x1b[?1002l") || !strings.Contains(out.String(), "\x1b[?1049l") {
		t.Fatalf("cleanup output did not disable mouse and alt-screen: %q", out.String())
	}
}
