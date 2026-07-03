package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

// envTemplate is the fresh ~/.axon/.env `axon setup` writes (0600). Secrets
// only; referenced from config as env:NAME.
const envTemplate = `# AXON secrets — referenced from config.yaml as env:NAME. chmod 600.
# Headless automations need a Claude Code OAuth token (once):
#     claude setup-token
# then paste the printed value here:
CLAUDE_CODE_OAUTH_TOKEN=
# NEVER set ANTHROPIC_API_KEY on subscription/enterprise installs — Claude
# Code would divert onto API billing. axon doctor warns if it finds one.
`

func newSetupCmd(gf *globalFlags) *cobra.Command {
	var vaultPath, profileName, provider string
	var withService bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "First-run provisioning: config, secrets, vault, index — interactively or via flags",
		Long: "The in-binary installer (used by install.sh, works standalone): asks for the\n" +
			"vault path, profile name, embeddings provider and service-at-login (or takes\n" +
			"--vault/--profile/--embeddings/--service), writes a starter config + .env if\n" +
			"absent (existing files are always kept), then converges everything with the\n" +
			"same idempotent steps as `axon init`. Safe to re-run any time.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			in := cmd.InOrStdin()
			st := ui.For(out)
			cfgPath := gf.configPath
			envPath := gf.envPath

			fmt.Fprintln(out, st.Header(ui.IconRocket, "axon setup"))

			_, cfgErr := os.Stat(cfgPath)
			configExists := cfgErr == nil

			// Gather choices (only needed when we are creating the config).
			if !configExists {
				if tui.Interactive(out) {
					var err error
					if vaultPath == "" {
						vaultPath, err = tui.Input(out, in, "Where is (or should be) your Obsidian vault?", "~/Notes/Vault", "~/Notes/Vault")
						if err != nil {
							return err
						}
					}
					if profileName == "" {
						profileName, err = tui.Input(out, in, "Profile name", "personal", "personal")
						if err != nil {
							return err
						}
					}
					if provider == "" {
						opts := []tui.Option{{Label: "Ollama (any pulled model, cross-platform)", Value: "ollama"}}
						if runtime.GOOS == "darwin" {
							opts = append([]tui.Option{{Label: "Apple on-device (no server; needs Xcode CLT)", Value: "apple"}}, opts...)
						}
						provider, err = tui.Select(out, in, "Embeddings provider", opts)
						if err != nil {
							return err
						}
					}
					if !cmd.Flags().Changed("service") {
						withService = tui.Confirm(out, in, "Start the AXON daemon at login (service install)?", true)
					}
				} else {
					if vaultPath == "" {
						return fmt.Errorf("no config at %s and no terminal — pass --vault <path> (and optionally --profile, --embeddings, --service)", cfgPath)
					}
					if profileName == "" {
						profileName = "personal"
					}
					if provider == "" {
						provider = "ollama"
					}
				}
				if provider != "ollama" && provider != "apple" {
					return fmt.Errorf("--embeddings must be ollama or apple (got %q)", provider)
				}

				starter, err := config.Starter(profileName, vaultPath, provider)
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(cfgPath, starter, 0o644); err != nil {
					return err
				}
				fmt.Fprintf(out, "%s created %s\n", st.Green(ui.IconOK), cfgPath)
			} else {
				fmt.Fprintf(out, "%s config exists: %s %s\n", st.Cyan(ui.IconAlready), cfgPath, st.Dim("(kept — converging it)"))
			}

			// Secrets file (kept if present; 0600 always).
			if _, err := os.Stat(envPath); os.IsNotExist(err) {
				if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(envPath, []byte(envTemplate), 0o600); err != nil {
					return err
				}
				fmt.Fprintf(out, "%s created %s %s\n", st.Green(ui.IconOK), envPath, st.Dim("(chmod 600 — add your claude setup-token)"))
			} else {
				fmt.Fprintf(out, "%s secrets exist: %s\n", st.Cyan(ui.IconAlready), envPath)
			}

			// Converge: the exact init flow (idempotent, verbose).
			_ = config.LoadDotEnv(envPath)
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			name, profile, err := cfg.ResolveProfile(gf.profile)
			if err != nil {
				return err
			}
			absCfg, err := filepath.Abs(cfgPath)
			if err != nil {
				absCfg = cfgPath
			}
			binary, _ := os.Executable()
			rep, err := core.Init(cmd.Context(), core.InitOptions{
				Config: cfg, ProfileName: name, Profile: profile,
				Out: out, ConfigPath: absCfg, BinaryPath: binary,
				ConvergeAppleLM: convergeAppleLM,
			})
			if err != nil {
				return err
			}
			if !rep.OK {
				return fmt.Errorf("setup: init reported blocking failures")
			}

			// Optional service-at-login.
			if withService {
				svcCmd, svcArgs := newRootCmd(), []string{"service", "install", "--config", cfgPath, "--env", envPath}
				if gf.profile != "" {
					svcArgs = append(svcArgs, "--profile", gf.profile)
				}
				svcCmd.SetArgs(svcArgs)
				svcCmd.SetOut(out)
				svcCmd.SetErr(cmd.ErrOrStderr())
				if err := svcCmd.Execute(); err != nil {
					fmt.Fprintf(out, "%s service install failed: %v %s\n", st.Yellow(ui.IconWarn), err, st.Dim("(run `axon service install` later)"))
				}
			}

			fmt.Fprintln(out, st.Divider(60))
			fmt.Fprintf(out, "%s %s\n", st.Green(ui.IconSpark), st.Bold("setup complete"))
			fmt.Fprintf(out, "%s next steps:\n", st.Cyan(ui.IconArrow))
			fmt.Fprintf(out, "   1. %s\n", st.Dim("claude login  (interactive auth) and claude setup-token → paste into "+envPath))
			fmt.Fprintf(out, "   2. %s\n", st.Dim("axon start  (or the installed service) — dashboard at http://127.0.0.1:"+fmt.Sprint(profile.Dashboard.Port)))
			fmt.Fprintf(out, "   3. %s\n", st.Dim("axon onboard — teach AXON who you are"))
			return nil
		},
	}
	cmd.Flags().StringVar(&vaultPath, "vault", "", "path of the Obsidian vault (created if missing)")
	cmd.Flags().StringVar(&profileName, "profile-name", "", "profile to create in a fresh config (default personal)")
	cmd.Flags().StringVar(&provider, "embeddings", "", "embeddings provider for a fresh config: ollama|apple")
	cmd.Flags().BoolVar(&withService, "service", false, "install the start-at-login service after provisioning")
	return cmd
}
