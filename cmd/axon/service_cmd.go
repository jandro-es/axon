package main

import (
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

func newServiceCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service <install|uninstall|print>",
		Short: "Emit/remove an OS service unit that supervises `axon start` (optional)",
		Long: "Generate a profile-scoped OS service unit (launchd on macOS, systemd --user\n" +
			"on Linux, Task Scheduler on Windows) so the daemon is supervised by the OS.\n" +
			"The core never depends on these (ADR-008); this only emits/installs them.",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"install", "uninstall", "print"},
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
				return nil
			case "uninstall":
				if err := os.Remove(unit.Path); err != nil && !os.IsNotExist(err) {
					return err
				}
				fmt.Fprintf(out, "%s removed %s\n  %s\n", st.Green(ui.IconOK), st.Cyan(unit.Path),
					st.Dim("(stop first if running: "+unit.StopCmd+")"))
				return nil
			default:
				return fmt.Errorf("unknown subcommand %q (use install|uninstall|print)", args[0])
			}
		},
	}
	return cmd
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}
