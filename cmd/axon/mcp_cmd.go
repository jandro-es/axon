package main

import (
	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/mcp"
)

func newMCPCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the AXON MCP server over stdio (launched by Claude Code)",
		Long: "Serve the AXON MCP tools (wikilink-safe vault ops, hybrid/knowledge search,\n" +
			"token status, automations) to Claude Code over stdio. Claude Code launches\n" +
			"this per the generated .claude/.mcp.json. Stdout/stdin carry the MCP protocol;\n" +
			"do not run interactively.",
		Args: cobra.NoArgs,
		// Silence cobra's own stdout: the MCP protocol owns stdout.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()
			return mcp.Serve(cmd.Context(), deps.mcpDeps(nil))
		},
	}
}
