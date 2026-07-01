package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/ui"
)

// Build metadata, overridden at build time via -ldflags "-X main.version=... -X
// main.commit=... -X main.date=...". The Makefile sets all three.
var (
	version = "0.0.0-dev"
	commit  = "none"
	date    = "unknown"
)

func newVersionCmd() *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the axon version and build metadata",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			out := cmd.OutOrStdout()
			st := ui.For(out)
			if short {
				fmt.Fprintf(out, "%s\n", version)
				return
			}
			fmt.Fprintf(out, "%s %s %s\n", ui.IconRocket, st.Bold("axon"), st.Cyan(version))
			fmt.Fprintf(out, "  %s %s\n", st.Dim("commit:"), commit)
			fmt.Fprintf(out, "  %s %s\n", st.Dim("built: "), date)
			fmt.Fprintf(out, "  %s %s %s/%s\n", st.Dim("go:    "), runtime.Version(), runtime.GOOS, runtime.GOARCH)
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print just the version string (for scripts)")
	return cmd
}

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
