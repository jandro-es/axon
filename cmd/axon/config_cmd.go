package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
)

func newConfigCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate configuration",
	}
	cmd.AddCommand(newConfigValidateCmd(gf))
	return cmd
}

func newConfigValidateCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the config file and the active profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(gf.configPath)
			if err != nil {
				return err
			}
			name, _, err := cfg.ResolveProfile(gf.profile)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"OK: %s is valid (%d profile(s); active profile %q)\n",
				gf.configPath, len(cfg.Profiles), name)
			return nil
		},
	}
}
