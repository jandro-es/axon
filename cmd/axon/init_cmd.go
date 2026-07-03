package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/tui"
)

func newInitCmd(gf *globalFlags) *cobra.Command {
	var asJSON bool
	var embeddingsChoice string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Provision the active profile: data dir, DB, vault scaffold, first index",
		Long: "Converge the active profile's environment idempotently: validate config,\n" +
			"run prerequisite checks, create the data dir and database, verify the\n" +
			"embedding model, scaffold the vault and build the first link-graph index.\n" +
			"Re-running reports what already exists and changes nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Persist an explicit provider choice BEFORE loading, so the init
			// below converges the chosen provider. Switching to apple also sets
			// the matching model/dim defaults (a later `axon reindex
			// --embeddings` re-vectorises the index).
			if embeddingsChoice != "" {
				if embeddingsChoice != "ollama" && embeddingsChoice != "apple" {
					return fmt.Errorf("--embeddings must be ollama or apple, got %q", embeddingsChoice)
				}
				if err := setConfigValue(gf.configPath, gf.profile, "embeddings.provider", embeddingsChoice); err != nil {
					return fmt.Errorf("persist embeddings provider: %w", err)
				}
				if embeddingsChoice == "apple" {
					if err := setConfigValue(gf.configPath, gf.profile, "embeddings.model", config.AppleEmbeddingModel); err != nil {
						return fmt.Errorf("persist embeddings model: %w", err)
					}
					if err := setConfigValue(gf.configPath, gf.profile, "embeddings.dim", strconv.Itoa(config.AppleEmbeddingDim)); err != nil {
						return fmt.Errorf("persist embeddings dim: %w", err)
					}
				}
			}

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
			absCfg, err := filepath.Abs(gf.configPath)
			if err != nil {
				absCfg = gf.configPath
			}
			binary, _ := os.Executable()
			opts := core.InitOptions{
				Config:          cfg,
				ProfileName:     name,
				Profile:         profile,
				Out:             out,
				ConfigPath:      absCfg,
				BinaryPath:      binary,
				ConvergeAppleLM: convergeAppleLM,
			}
			if asJSON {
				opts.Out = nil // suppress streaming text; emit JSON only
			}

			// Live step view on a TTY; the plain streamed report (canonical
			// output) is unchanged everywhere else.
			if !asJSON && tui.Interactive(out) {
				steps := tui.NewSteps(out, fmt.Sprintf("axon init — profile %q", name), nil)
				steps.Start()
				opts.Out = io.Discard
				opts.OnStep = func(s core.StepResult) {
					steps.Set(s.Name, s.Detail, tui.StepStatus(s.Status))
				}
				rep, runErr := core.Init(cmd.Context(), opts)
				summary := "environment converged"
				if !rep.Changed {
					summary = "no changes, already converged"
				}
				_ = steps.Finish(summary)
				if runErr != nil {
					return runErr
				}
				if !rep.OK {
					return fmt.Errorf("init completed with blocking failures")
				}
				return nil
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
	cmd.Flags().StringVar(&embeddingsChoice, "embeddings", "",
		"select the embeddings provider (ollama|apple) and persist it to config before converging")
	return cmd
}
