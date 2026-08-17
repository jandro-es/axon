package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/service"
	"github.com/jandro-es/axon/internal/ui"
)

// serviceStatusJSON is what `axon service status --json` emits. It exists so
// GUI clients can show a "start at login" toggle without stat-ing plists or
// shelling to launchctl themselves — the CLI owns service semantics.
type serviceStatusJSON struct {
	Profile   string `json:"profile"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	Supported bool   `json:"supported"`
	// PathEnv is the PATH the unit hands the daemon, resolved from the
	// installing shell's full PATH. Clients that start with the minimal system
	// PATH — a GUI app launched by LaunchServices, say — use it so the tools
	// they spawn see the same machine the user's shell does. Falls back to the
	// PATH this binary would generate now when no unit is installed.
	PathEnv string `json:"path_env,omitempty"`
}

func newServiceCmd(gf *globalFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "service <install|uninstall|print|status>",
		Short: "Emit/remove an OS service unit that supervises `axon start` (optional)",
		Long: "Generate a profile-scoped OS service unit (launchd on macOS, systemd --user\n" +
			"on Linux, Task Scheduler on Windows) so the daemon is supervised by the OS.\n" +
			"The core never depends on these (ADR-008); this only emits/installs them.",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"install", "uninstall", "print", "status"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(gf.configPath)
			if err != nil {
				return err
			}
			name, profile, err := cfg.ResolveProfile(gf.profile)
			if err != nil {
				return err
			}
			paths := profile.Paths()
			absCfg, _ := filepath.Abs(gf.configPath)
			absEnv, _ := filepath.Abs(gf.envPath)
			binary, _ := os.Executable()

			unit, err := service.ForOS(runtime.GOOS, service.Params{
				Profile:    name,
				Binary:     binary,
				ConfigPath: absCfg,
				EnvPath:    absEnv,
				ConfigDir:  paths.ConfigDir,
				AxonHome:   config.AxonHome(),
				LogDir:     paths.LogsDir,
				HomeDir:    homeDir(),
				PathEnv:    service.DaemonPathEnv(exec.LookPath),
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			st := ui.For(out)
			switch args[0] {
			case "status":
				content, statErr := os.ReadFile(unit.Path)
				pathEnv := service.UnitPathEnv(unit.Kind, string(content))
				if pathEnv == "" {
					// No unit, or one predating the embedded PATH: report what
					// installing now would produce, which is still better than
					// nothing for a caller trying to find the user's tools.
					pathEnv = service.DaemonPathEnv(exec.LookPath)
				}
				status := serviceStatusJSON{
					PathEnv:   pathEnv,
					Profile:   name,
					Kind:      unit.Kind,
					Path:      unit.Path,
					Installed: statErr == nil,
					Supported: unit.Path != "",
				}
				if asJSON {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					return enc.Encode(status)
				}
				if status.Installed {
					fmt.Fprintf(out, "%s %s unit installed: %s\n",
						st.Green(ui.IconOK), unit.Kind, st.Cyan(unit.Path))
				} else {
					fmt.Fprintf(out, "%s no %s unit installed — the daemon does not start at login\n",
						st.Dim(ui.IconWarn), unit.Kind)
					fmt.Fprintf(out, "  %s %s\n", st.Dim("install with:"), st.Bold("axon service install"))
				}
				return nil
			case "print":
				// The unit file content is emitted RAW so it can be piped straight to
				// the real unit path; only the trailing how-to comment is styled.
				fmt.Fprint(out, unit.Content)
				fmt.Fprint(out, st.Dim(fmt.Sprintf("\n# install path: %s\n# enable: %s\n# start:  %s\n# stop:   %s\n",
					unit.Path, unit.EnableCmd, unit.StartCmd, unit.StopCmd)))
				return nil
			case "install":
				if err := os.MkdirAll(filepath.Dir(unit.Path), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(unit.Path, []byte(unit.Content), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(out, "%s wrote %s unit: %s\n", st.Green(ui.IconOK), unit.Kind, st.Cyan(unit.Path))
				fmt.Fprintf(out, "  %s\n    %s\n    %s\n", st.Dim("enable & start with:"),
					st.Bold(unit.EnableCmd), st.Bold(unit.StartCmd))
				// Rewriting the unit does not reach a job already loaded from
				// the previous one — it keeps the environment it was loaded
				// with until something boots it out. Say so here, where the
				// user has just rewritten it.
				if unit.ReloadCmd != "" {
					fmt.Fprintf(out, "  %s\n    %s\n", st.Dim("already running? it keeps the OLD unit until reloaded:"),
						st.Bold(unit.ReloadCmd))
				}
				return nil
			case "uninstall":
				if err := os.Remove(unit.Path); err != nil && !os.IsNotExist(err) {
					return err
				}
				fmt.Fprintf(out, "%s removed %s\n  %s\n", st.Green(ui.IconOK), st.Cyan(unit.Path),
					st.Dim("(stop first if running: "+unit.StopCmd+")"))
				return nil
			default:
				return fmt.Errorf("unknown subcommand %q (use install|uninstall|print|status)", args[0])
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit `service status` as JSON")
	return cmd
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}
