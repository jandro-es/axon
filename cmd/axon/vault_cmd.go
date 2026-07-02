package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

func newVaultCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Vault-level operations (move)",
	}
	cmd.AddCommand(newVaultMoveCmd(gf))
	return cmd
}

func newVaultMoveCmd(gf *globalFlags) *cobra.Command {
	var stopDaemon, asJSON bool
	cmd := &cobra.Command{
		Use:   "move <new-path>",
		Short: "Move the vault to a new location and update every reference AXON owns",
		Long: "Relocates the vault directory (rename, or copy+verify across filesystems),\n" +
			"updates vault_path in the config, and regenerates the .claude/ wiring at the\n" +
			"new location. The search index needs nothing (it stores vault-relative\n" +
			"paths). Refuses while the daemon runs — it offers to stop it interactively,\n" +
			"or pass --stop-daemon in scripts. One thing AXON cannot update: Obsidian's\n" +
			"own vault bookmark — open the vault at its new location once.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			st := ui.For(out)
			_ = config.LoadDotEnv(gf.envPath)
			cfg, err := config.Load(gf.configPath)
			if err != nil {
				return err
			}
			name, profile, err := cfg.ResolveProfile(gf.profile)
			if err != nil {
				return err
			}
			paths := profile.Paths()

			// The daemon holds the DB and watches the vault: never move under it.
			if pid, perr := readPidFile(paths.DataDir); perr == nil && processAlive(pid) {
				proceed := stopDaemon
				if !proceed && tui.Interactive(out) {
					proceed = tui.Confirm(out, cmd.InOrStdin(),
						fmt.Sprintf("The daemon is running (pid %d). Stop it and continue the move?", pid), false)
				}
				if !proceed {
					return fmt.Errorf("daemon is running (pid %d) — stop it first (`axon stop`) or pass --stop-daemon", pid)
				}
				if err := signalStop(pid); err != nil {
					return fmt.Errorf("stop daemon: %w", err)
				}
				for i := 0; i < 50 && processAlive(pid); i++ {
					time.Sleep(100 * time.Millisecond)
				}
				fmt.Fprintf(out, "%s stopped the daemon (pid %d) — restart it after the move (`axon start` or the service)\n", st.Yellow(ui.IconWarn), pid)
			}

			absCfg, err := filepath.Abs(gf.configPath)
			if err != nil {
				absCfg = gf.configPath
			}
			binary, _ := os.Executable()

			rep, err := core.MoveVault(cmd.Context(), core.VaultMoveOptions{
				ProfileName: name,
				Profile:     profile,
				Dest:        args[0],
				ConfigPath:  absCfg,
				BinaryPath:  binary,
				SetConfig: func(key, value string) error {
					return setConfigValue(gf.configPath, gf.profile, key, value)
				},
			})
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if jerr := enc.Encode(rep); jerr != nil {
					return jerr
				}
				return err
			}
			for _, s := range rep.Steps {
				glyph := st.Green(ui.IconOK)
				switch s.Status {
				case core.StepAlready:
					glyph = st.Cyan(ui.IconAlready)
				case core.StepWarn:
					glyph = st.Yellow(ui.IconWarn)
				case core.StepFailed:
					glyph = st.Red(ui.IconError)
				}
				fmt.Fprintf(out, "%s %-14s %s\n", glyph, s.Name, st.Dim(s.Detail))
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s\n", st.Green(ui.IconSpark), st.Bold("vault moved to "+rep.To))
			fmt.Fprintf(out, "%s %s\n", st.Yellow(ui.IconArrow),
				st.Dim("Obsidian still points at the old path — use \"Open folder as vault\" once at the new location."))
			return nil
		},
	}
	cmd.Flags().BoolVar(&stopDaemon, "stop-daemon", false, "stop a running daemon before moving (scripts)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the step report as JSON")
	return cmd
}
