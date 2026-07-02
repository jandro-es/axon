package tui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
)

// Option is one selectable choice for Select.
type Option struct {
	Label string
	Value string
	Hint  string
}

// Confirm asks a yes/no question. Non-interactive writers never prompt: the
// caller-provided default is returned so scripted runs behave predictably.
func Confirm(out io.Writer, in io.Reader, prompt string, defaultYes bool) bool {
	if !Interactive(out) {
		return defaultYes
	}
	v := defaultYes
	err := runForm(out, in, huh.NewConfirm().Title(prompt).Value(&v))
	if err != nil {
		return defaultYes
	}
	return v
}

// TypedConfirm requires the user to type phrase exactly (destructive ops).
// Non-interactive: always false — a script can never stumble into destruction.
func TypedConfirm(out io.Writer, in io.Reader, prompt, phrase string) bool {
	if !Interactive(out) {
		return false
	}
	var typed string
	err := runForm(out, in, huh.NewInput().
		Title(prompt).
		Description(fmt.Sprintf("Type %q to confirm.", phrase)).
		Value(&typed))
	return err == nil && typed == phrase
}

// Select presents a single-choice menu and returns the chosen Value.
// Non-interactive: an error — there is no safe default for a menu.
func Select(out io.Writer, in io.Reader, title string, options []Option) (string, error) {
	if !Interactive(out) {
		return "", fmt.Errorf("%q requires a terminal (use the equivalent subcommand/flags in scripts)", title)
	}
	opts := make([]huh.Option[string], 0, len(options))
	for _, o := range options {
		label := o.Label
		if o.Hint != "" {
			label = fmt.Sprintf("%s (%s)", o.Label, o.Hint)
		}
		opts = append(opts, huh.NewOption(label, o.Value))
	}
	var choice string
	if err := runForm(out, in, huh.NewSelect[string]().Title(title).Options(opts...).Value(&choice)); err != nil {
		return "", err
	}
	return choice, nil
}

// Input asks for a free-text value with a default. Non-interactive: returns def.
func Input(out io.Writer, in io.Reader, title, placeholder, def string) (string, error) {
	if !Interactive(out) {
		return def, nil
	}
	v := def
	if err := runForm(out, in, huh.NewInput().Title(title).Placeholder(placeholder).Value(&v)); err != nil {
		return "", err
	}
	if v == "" {
		return def, nil
	}
	return v, nil
}

// runForm wraps a single huh field in a form bound to out/in.
func runForm(out io.Writer, in io.Reader, field huh.Field) error {
	return huh.NewForm(huh.NewGroup(field)).WithOutput(out).WithInput(in).Run()
}
