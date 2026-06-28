package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the axon version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "axon %s\n", version)
		},
	}
}

// newStubCmds returns the commands that are part of the CLI surface but not yet
// implemented in Phase 0. Each names the phase that will deliver it, so the
// skeleton is honest about scope rather than silently absent.
func newStubCmds(_ *globalFlags) []*cobra.Command {
	stubs := []struct {
		use, short, phase string
	}{
		{"stop", "Stop the daemon (use Ctrl-C, or `axon service` for OS supervision)", "Phase 7"},
	}
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
