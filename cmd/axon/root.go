package main

import (
	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
)

// globalFlags holds the persistent flags shared by every subcommand.
type globalFlags struct {
	configPath string
	profile    string
	envPath    string
}

func newRootCmd() *cobra.Command {
	gf := &globalFlags{}

	root := &cobra.Command{
		Use:           "axon",
		Short:         "AXON — a local-first AI operating system for an Obsidian vault",
		Long:          "AXON turns an Obsidian vault into a self-maintaining second brain.\nThe full command set is implemented; run `axon <command> --help` for details.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	pf := root.PersistentFlags()
	pf.StringVarP(&gf.configPath, "config", "c", config.DefaultConfigPath(), "path to the config file (default ~/.axon/config.yaml)")
	pf.StringVarP(&gf.profile, "profile", "p", "", "active profile (overrides config + AXON_PROFILE)")
	pf.StringVar(&gf.envPath, "env", ".env", "path to the .env secrets file")

	root.AddCommand(newConfigCmd(gf), newDoctorCmd(gf), newVersionCmd())
	root.AddCommand(newInitCmd(gf), newReindexCmd(gf), newOnboardCmd(gf))
	root.AddCommand(newIngestCmd(gf), newSearchCmd(gf), newStatusCmd(gf))
	root.AddCommand(newRunCmd(gf), newStartCmd(gf), newStopCmd(gf))
	root.AddCommand(newMCPCmd(gf), newHookCmd(gf))
	root.AddCommand(newServiceCmd(gf), newExportCmd(gf), newProfilesCmd(gf))
	root.AddCommand(newStubCmds(gf)...)
	return root
}
