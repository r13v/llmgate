package system

import (
	"os"

	"golang.org/x/term"
)

type Terminal interface {
	IsInteractive() bool
}

type RealTerminal struct {
	Stdin  *os.File
	Stdout *os.File
}

func (t RealTerminal) IsInteractive() bool {
	if t.Stdin == nil || t.Stdout == nil {
		return false
	}
	return term.IsTerminal(int(t.Stdin.Fd())) && term.IsTerminal(int(t.Stdout.Fd()))
}
