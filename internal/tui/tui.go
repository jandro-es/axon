// Package tui is AXON's Charm-based terminal UI layer (ADR-014): live step
// lists, spinners, styled tables and huh forms. Every surface degrades to the
// plain rendering contract when the destination is not an interactive
// terminal, so daemons, scripts, --json and CI never see control sequences and
// never block on a prompt. Commands keep their plain renderers as the
// canonical output and route the same structured data here on a TTY.
package tui

import (
	"io"
	"os"

	"golang.org/x/term"
)

// Interactive reports whether live TUI rendering is allowed on w. It is the
// single gate that keeps headless paths safe: CI, pipes, buffers and files
// are never interactive.
func Interactive(w io.Writer) bool {
	if os.Getenv("CI") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
