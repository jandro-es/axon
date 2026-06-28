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
		{"init", "Provision a vault + data dir from config", "Phase 1"},
		{"reindex", "Rebuild SQLite from the vault", "Phase 1"},
		{"ingest", "Ingest a URL/PDF into the knowledge base", "Phase 2"},
		{"search", "Hybrid search across the vault + knowledge", "Phase 2"},
		{"status", "Show budget, runs and health", "Phase 3"},
		{"run", "Run an automation on demand", "Phase 4"},
		{"start", "Start the daemon", "Phase 4"},
		{"stop", "Stop the daemon", "Phase 4"},
		{"mcp", "Run the AXON MCP server over stdio", "Phase 5"},
		{"export", "Export a portable context snapshot", "Phase 4"},
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
