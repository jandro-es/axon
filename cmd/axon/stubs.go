package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newStubCmds returns CLI-surface commands that are not yet implemented. The
// full command set is now built, so this list is empty — the function is kept as
// the single, honest place to declare any future not-yet-implemented command.
func newStubCmds(_ *globalFlags) []*cobra.Command {
	stubs := []struct {
		use, short, phase string
	}{}
	cmds := make([]*cobra.Command, 0, len(stubs))
	for _, s := range stubs {
		phase := s.phase
		cmds = append(cmds, &cobra.Command{
			Use:   s.use,
			Short: s.short + " (not yet implemented — " + s.phase + ")",
			RunE: func(cmd *cobra.Command, _ []string) error {
				return fmt.Errorf("%q is not yet implemented (planned for %s)", cmd.Name(), phase)
			},
		})
	}
	return cmds
}
