package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/clients"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/mcp"
)

func newMCPCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the AXON MCP server over stdio (launched by Claude clients)",
		Long: "Serve the AXON MCP tools (wikilink-safe vault ops, hybrid/knowledge search,\n" +
			"token status, automations, memory) to a Claude client over stdio. Claude Code\n" +
			"and Claude Desktop both launch this per their generated config. Stdout/stdin\n" +
			"carry the MCP protocol; do not run interactively. Use `axon mcp install` to\n" +
			"register the server with a client.",
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
	cmd.AddCommand(newMCPInstallCmd(gf))
	return cmd
}

// newMCPInstallCmd registers the AXON MCP server with a Claude client (FR-74).
// For `code` it (re)generates the project `.claude/` wiring; for `desktop` it
// merges a profile-scoped entry into claude_desktop_config.json non-destructively.
func newMCPInstallCmd(gf *globalFlags) *cobra.Command {
	var client string
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "install --client <code|desktop>",
		Short: "Register the AXON MCP server with a Claude client",
		Long: "Wire the AXON MCP server into a Claude client:\n" +
			"  --client code     (re)generate the project .claude/ wiring (CLAUDE.md, .mcp.json,\n" +
			"                     settings.json, plugin) — what `axon init` writes.\n" +
			"  --client desktop  merge a profile-scoped entry into claude_desktop_config.json\n" +
			"                     non-destructively (other servers are preserved).\n\n" +
			"Claude Desktop receives AXON's tools only — no hooks, skills, subagents or\n" +
			"headless automations (those are Claude Code). AXON's own tools stay wikilink-safe\n" +
			"in the server regardless of client. Use --print to preview the registration JSON.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, err := loadProfileDeps(gf, false) // wiring needs the vault/paths only
			if err != nil {
				return err
			}
			defer deps.close()
			out := cmd.OutOrStdout()

			absCfg, err := filepath.Abs(gf.configPath)
			if err != nil {
				absCfg = gf.configPath
			}
			binary, _ := os.Executable()
			if binary == "" {
				binary = "axon"
			}
			p := clients.Params{
				Profile:    deps.name,
				Binary:     binary,
				ConfigPath: absCfg,
				ConfigDir:  deps.paths.ConfigDir,
				AxonHome:   config.AxonHome(),
			}

			if printOnly {
				fmt.Fprint(out, p.PrintJSON())
				return nil
			}

			switch client {
			case "code":
				res, werr := ensureClaudeWiring(deps, gf)
				if werr != nil {
					return werr
				}
				if len(res.Created) > 0 {
					fmt.Fprintf(out, "Claude Code: wrote %d .claude file(s) under %s\n", len(res.Created), deps.paths.VaultPath)
				} else {
					fmt.Fprintf(out, "Claude Code: wiring already present under %s\n", deps.paths.VaultPath)
				}
				fmt.Fprintln(out, "Open Claude Code in the vault to use the AXON tools, hooks and skills.")
				return nil

			case "desktop":
				cfgPath := os.Getenv("AXON_DESKTOP_CONFIG") // test/override seam
				if cfgPath == "" {
					cfgPath, err = clients.DesktopConfigPath()
					if err != nil {
						return err
					}
				}
				r, ierr := clients.InstallDesktop(cfgPath, p)
				if ierr != nil {
					return ierr
				}
				fmt.Fprintf(out, "Claude Desktop: %s %s (profile %q)\n", r.Action, r.Path, deps.name)
				if r.Action != "unchanged" {
					fmt.Fprintln(out, "Restart Claude Desktop to load the AXON tools.")
				}
				fmt.Fprintln(out, "Note: Desktop gets AXON's tools only — no hooks/skills/profile injection.")
				fmt.Fprintln(out, "Keep all vault edits in the AXON tools (they stay wikilink-safe).")
				return nil

			default:
				return fmt.Errorf("unknown --client %q (use code|desktop)", client)
			}
		},
	}
	cmd.Flags().StringVar(&client, "client", "", "client to wire: code|desktop (required unless --print)")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the registration JSON instead of writing it")
	return cmd
}
