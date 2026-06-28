package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
)

func newInitCmd(gf *globalFlags) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Provision the active profile: data dir, DB, vault scaffold, first index",
		Long: "Converge the active profile's environment idempotently: validate config,\n" +
			"run prerequisite checks, create the data dir and database, verify the\n" +
			"embedding model, scaffold the vault and build the first link-graph index.\n" +
			"Re-running reports what already exists and changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_ = config.LoadDotEnv(gf.envPath)
			cfg, err := config.Load(gf.configPath)
			if err != nil {
				return err
			}
			name, profile, err := cfg.ResolveProfile(gf.profile)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			opts := core.InitOptions{
				Config:      cfg,
				ProfileName: name,
				Profile:     profile,
				Out:         out,
			}
			if asJSON {
				opts.Out = nil // suppress streaming text; emit JSON only
			}

			rep, runErr := core.Init(cmd.Context(), opts)

			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(rep); err != nil {
					return err
				}
			}
			if runErr != nil {
				return runErr
			}
			if !rep.OK {
				return fmt.Errorf("init completed with blocking failures")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable step results as JSON")
	return cmd
}
