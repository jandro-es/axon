package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/service"
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
			binary, _ := os.Executable()

			unit, err := service.ForOS(runtime.GOOS, service.Params{
				Profile:    name,
				Binary:     binary,
				ConfigPath: absCfg,
				ConfigDir:  paths.ConfigDir,
				AxonHome:   config.AxonHome(),
				LogDir:     paths.LogsDir,
				HomeDir:    homeDir(),
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			switch args[0] {
			case "print":
				fmt.Fprint(out, unit.Content)
				fmt.Fprintf(out, "\n# install path: %s\n# enable: %s\n# start:  %s\n# stop:   %s\n",
					unit.Path, unit.EnableCmd, unit.StartCmd, unit.StopCmd)
				return nil
			case "install":
				if err := os.MkdirAll(filepath.Dir(unit.Path), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(unit.Path, []byte(unit.Content), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(out, "✓ wrote %s unit: %s\n", unit.Kind, unit.Path)
				fmt.Fprintf(out, "  enable & start with:\n    %s\n    %s\n", unit.EnableCmd, unit.StartCmd)
				return nil
			case "uninstall":
				if err := os.Remove(unit.Path); err != nil && !os.IsNotExist(err) {
					return err
				}
				fmt.Fprintf(out, "✓ removed %s\n  (stop first if running: %s)\n", unit.Path, unit.StopCmd)
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
