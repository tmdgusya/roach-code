//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || aix || zos)

package cli

import (
	"errors"

	tea "charm.land/bubbletea/v2"
)

var (
	errTerminalDetached   = errors.New("terminal detached while roach was reading input")
	errTerminalTerminated = errors.New("terminal session terminated")
)

type teaProgram interface {
	Run() (tea.Model, error)
}

func runTeaProgramWithTerminalFailsafe(p teaProgram) (tea.Model, error) {
	return p.Run()
}
