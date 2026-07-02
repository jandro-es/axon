package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/service"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

// newUninstallCmd removes AXON from the machine: daemon, service unit, binary,
// and (only with an explicit, confirmed --purge) the AXON home. The VAULT is
// never touched — it is the user's knowledge, not AXON's state.
func newUninstallCmd(gf *globalFlags) *cobra.Command {
	var purge, yesPurge, asJSON bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove AXON: daemon, service, binary (--purge also removes ~/.axon; the vault is NEVER touched)",
		Args:  cobra.NoArgs,
		Long: "Stops the daemon, removes the start-at-login service unit and the binary.\n" +
			"--purge additionally deletes the AXON home (config, secrets, per-profile\n" +
			"databases) after a typed confirmation (headless: --yes-purge-all-data).\n" +
			"Your vault — the Markdown knowledge base — is never touched by any path.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			st := ui.For(out)
			type step struct {
				Name, Status, Detail string
			}
			var steps []step
			report := func(name, status, detail string) {
				steps = append(steps, step{name, status, detail})
				if asJSON {
					return
				}
				glyph := st.Green(ui.IconOK)
				switch status {
				case "already":
					glyph = st.Cyan(ui.IconAlready)
				case "warn":
					glyph = st.Yellow(ui.IconWarn)
				}
				fmt.Fprintf(out, "%s %-10s %s\n", glyph, name, st.Dim(detail))
			}

			if !asJSON {
				fmt.Fprintln(out, st.Header(ui.IconWrench, "axon uninstall"))
			}

			// Best-effort profile resolution: uninstall must work even with a
			// broken config (we still need the data dir for the pidfile).
			var dataDir, profileName string
			if cfg, err := config.Load(gf.configPath); err == nil {
				if name, p, rerr := cfg.ResolveProfile(gf.profile); rerr == nil {
					profileName, dataDir = name, p.Paths().DataDir
				}
			}

			// 1. Stop the daemon.
			if dataDir != "" {
				if pid, err := readPidFile(dataDir); err == nil && processAlive(pid) {
					if err := signalStop(pid); err != nil {
						report("daemon", "warn", fmt.Sprintf("could not stop pid %d: %v", pid, err))
					} else {
						// Give it a moment to exit cleanly before files disappear.
						for i := 0; i < 20 && processAlive(pid); i++ {
							time.Sleep(100 * time.Millisecond)
						}
						report("daemon", "done", fmt.Sprintf("stopped (pid %d)", pid))
					}
				} else {
					report("daemon", "already", "not running")
				}
			} else {
				report("daemon", "already", "no profile resolvable — skipped pid check")
			}

			// 2. Remove the service unit (same path `axon service uninstall` uses).
			if profileName != "" {
				unit, err := service.ForOS(runtime.GOOS, service.Params{Profile: profileName, HomeDir: homeDir()})
				if err == nil {
					if _, serr := os.Stat(unit.Path); serr == nil {
						unloadServiceUnit(unit)
						if rerr := os.Remove(unit.Path); rerr != nil {
							report("service", "warn", fmt.Sprintf("could not remove %s: %v", unit.Path, rerr))
						} else {
							report("service", "done", "removed "+unit.Path)
						}
					} else {
						report("service", "already", "no service unit installed")
					}
				}
			} else {
				report("service", "already", "no profile resolvable — skipped")
			}

			// 3. Purge AXON home (explicitly opted in + confirmed; never the vault).
			if purge {
				home := config.AxonHome()
				confirmed := yesPurge
				if !confirmed {
					confirmed = tui.TypedConfirm(out, cmd.InOrStdin(),
						fmt.Sprintf("Delete ALL AXON data under %s? (config, secrets, databases — your vault is untouched)", home), "purge")
				}
				if !confirmed {
					report("purge", "warn", "not confirmed — data kept (headless runs need --yes-purge-all-data)")
				} else if err := os.RemoveAll(home); err != nil {
					report("purge", "warn", fmt.Sprintf("could not remove %s: %v", home, err))
				} else {
					report("purge", "done", "removed "+home)
				}
			} else {
				report("data", "already", "kept (config, databases; remove with --purge)")
			}

			// 4. The binary last — it may be running us. Only ever an installed
			// `axon` binary: anything else (a test binary, a renamed build) is
			// left alone with instructions.
			bin, err := os.Executable()
			if err == nil {
				if filepath.Base(bin) != "axon" {
					report("binary", "already", fmt.Sprintf("running as %s (not an installed axon binary) — nothing removed", filepath.Base(bin)))
				} else if rerr := os.Remove(bin); rerr != nil {
					report("binary", "warn", fmt.Sprintf("could not remove %s — run: sudo rm %q", bin, bin))
				} else {
					report("binary", "done", "removed "+bin)
				}
			}

			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(steps)
			}
			fmt.Fprintf(out, "%s %s\n", st.Green(ui.IconSpark), st.Bold("AXON removed — your vault is exactly where it was"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also delete the AXON home (config, secrets, databases)")
	cmd.Flags().BoolVar(&yesPurge, "yes-purge-all-data", false, "confirm --purge without a prompt (scripts)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the step report as JSON")
	return cmd
}

// unloadServiceUnit best-effort tells the supervisor to forget the service
// before its file is removed. Direct argv — never a shell — so nothing in the
// unit path can be interpreted. Failures are fine; removing the file is the
// authoritative act.
func unloadServiceUnit(unit service.Unit) {
	switch unit.Kind {
	case "launchd":
		_ = exec.Command("launchctl", "unload", unit.Path).Run()
	case "systemd":
		_ = exec.Command("systemctl", "--user", "disable", "--now", filepath.Base(unit.Path)).Run()
	}
}
