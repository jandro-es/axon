// Package ui provides small, dependency-free terminal styling for AXON's CLI:
// ANSI colours, status glyphs and emoji, plus a consistent way to render errors
// with actionable hints. Styling is applied only when the destination is a real
// terminal and colour is not disabled (NO_COLOR / TERM=dumb), so piped output,
// redirected files and tests stay plain text and machine-parseable.
//
// The package intentionally imports nothing from the rest of the module — it is
// a leaf that both cmd and core can depend on without creating import cycles.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// ANSI SGR sequences. Kept unexported; reach them through Styler methods so
// styling is always gated on whether colour is enabled.
const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	blue   = "\033[34m"
	cyan   = "\033[36m"
	gray   = "\033[90m"
)

// Status glyphs used across command output. Chosen to read clearly in a
// terminal and to pair with a colour from the matching Styler method.
const (
	IconOK      = "✓" // success / created
	IconAlready = "↻" // already present, nothing to do
	IconWarn    = "⚠" // non-fatal issue / soft check
	IconError   = "✗" // blocking failure
	IconInfo    = "•" // neutral information
	IconArrow   = "→" // a next step / pointer
	IconSearch  = "🔎"
	IconWrench  = "🔧"
	IconDoctor  = "🩺"
	IconSpark   = "✨"
	IconRocket  = "🚀"
	IconChart   = "📊"
	IconBoom    = "💥"
)

// Styler applies ANSI styling, but only when enabled. When disabled every method
// returns its input unchanged, so non-terminal output stays plain.
type Styler struct{ on bool }

// For returns a Styler configured for w: styling is enabled only when w is a
// terminal, NO_COLOR is unset and TERM is not "dumb".
func For(w io.Writer) Styler { return Styler{on: colorEnabled(w)} }

// Enabled reports whether this Styler will emit colour.
func (s Styler) Enabled() bool { return s.on }

// colorEnabled applies the standard heuristics for deciding whether to colour
// output written to w. NO_COLOR always wins; FORCE_COLOR overrides terminal
// detection (useful when piping into a pager like `less -R`); otherwise colour
// is used only for a real terminal.
func colorEnabled(w io.Writer) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	if _, ok := os.LookupEnv("FORCE_COLOR"); ok {
		return true
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func (s Styler) wrap(seq, str string) string {
	if !s.on || str == "" {
		return str
	}
	return seq + str + reset
}

// Colour / weight helpers. Each is a no-op when styling is disabled.
func (s Styler) Bold(str string) string   { return s.wrap(bold, str) }
func (s Styler) Dim(str string) string    { return s.wrap(dim, str) }
func (s Styler) Red(str string) string    { return s.wrap(red, str) }
func (s Styler) Green(str string) string  { return s.wrap(green, str) }
func (s Styler) Yellow(str string) string { return s.wrap(yellow, str) }
func (s Styler) Blue(str string) string   { return s.wrap(blue, str) }
func (s Styler) Cyan(str string) string   { return s.wrap(cyan, str) }
func (s Styler) Gray(str string) string   { return s.wrap(gray, str) }

// Divider returns a horizontal rule of n box-drawing runes, dimmed when styling
// is on so it recedes behind the content.
func (s Styler) Divider(n int) string { return s.Dim(strings.Repeat("─", n)) }

// Header renders a title line with an optional leading emoji and a bold title.
func (s Styler) Header(emoji, title string) string {
	if emoji == "" {
		return s.Bold(title)
	}
	return emoji + "  " + s.Bold(title)
}

// FprintError renders err to w (typically os.Stderr) as a clear, coloured block
// with a leading ✗, followed by an actionable hint when one is known. It is the
// single place the CLI turns an error into human-facing text.
func FprintError(w io.Writer, err error) {
	if err == nil {
		return
	}
	s := For(w)
	fmt.Fprintf(w, "\n%s %s %s\n", s.Red(IconError), s.Bold(s.Red("Error:")), err.Error())
	if h := Hint(err); h != "" {
		fmt.Fprintf(w, "%s %s\n", s.Yellow(IconArrow), s.Dim(h))
	}
	fmt.Fprintln(w)
}
