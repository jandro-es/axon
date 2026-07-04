package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"
	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

// newConfigureCmd is the simple-switching surface (spec: operations overhaul,
// Component 3): an interactive menu on a TTY, scriptable subcommands
// everywhere. Every edit goes through the comment-preserving setConfigValue.
func newConfigureCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Change common settings simply (embeddings provider, models, budgets, automations)",
		Long: "Interactive menu on a terminal; scriptable subcommands everywhere:\n" +
			"  axon configure embeddings <ollama|apple> [--model M --dim N] [--reindex]\n" +
			"  axon configure models <classify|routine|synthesis> <model>\n" +
			"  axon configure limits <daily|weekly> <tokens>\n" +
			"  axon configure automations <name> <on|off>\n" +
			"  axon configure dashboard-port <port>\n" +
			"Edits preserve comments and are re-validated before writing.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if !tui.Interactive(out) {
				// Headless: never hang on a menu — print the scriptable surface.
				return cmd.Help()
			}
			return configureMenu(cmd, gf)
		},
	}
	cmd.AddCommand(newConfigureEmbeddingsCmd(gf), newConfigureModelsCmd(gf),
		newConfigureLimitsCmd(gf), newConfigureAutomationsCmd(gf), newConfigureDashboardPortCmd(gf))
	return cmd
}

// configureMenu is the interactive loop.
func configureMenu(cmd *cobra.Command, gf *globalFlags) error {
	out := cmd.OutOrStdout()
	in := cmd.InOrStdin()
	for {
		cfg, err := config.Load(gf.configPath)
		if err != nil {
			return err
		}
		_, p, err := cfg.ResolveProfile(gf.profile)
		if err != nil {
			return err
		}
		choice, err := tui.Select(out, in, "What do you want to change?", []tui.Option{
			{Label: "Embeddings provider", Value: "embeddings", Hint: p.Embeddings.Provider},
			{Label: "Models per operation class", Value: "models", Hint: p.Models.Synthesis},
			{Label: "Token budgets", Value: "limits", Hint: fmt.Sprintf("day %d", p.Limits.DailyTokens.Int())},
			{Label: "Automations on/off", Value: "automations"},
			{Label: "Dashboard port", Value: "dashboard", Hint: strconv.Itoa(p.Dashboard.Port)},
			{Label: "Done", Value: "done"},
		})
		if err != nil {
			return err
		}
		switch choice {
		case "done":
			return nil
		case "embeddings":
			target, err := tui.Select(out, in, "Embeddings provider", []tui.Option{
				{Label: "Apple on-device (macOS, no server)", Value: "apple"},
				{Label: "Ollama (any pulled model, cross-platform)", Value: "ollama"},
			})
			if err != nil {
				return err
			}
			if target == p.Embeddings.Provider {
				fmt.Fprintf(out, "%s already using %s\n", ui.IconAlready, target)
				continue
			}
			if err := switchEmbeddings(cmd, gf, target, "", 0, false); err != nil {
				return err
			}
		case "models":
			class, err := tui.Select(out, in, "Which operation class?", []tui.Option{
				{Label: "classify (cheapest)", Value: "classify", Hint: p.Models.Classify},
				{Label: "routine", Value: "routine", Hint: p.Models.Routine},
				{Label: "synthesis (most capable)", Value: "synthesis", Hint: p.Models.Synthesis},
			})
			if err != nil {
				return err
			}
			model, err := askModelForClass(out, in, p, class)
			if err != nil {
				return err
			}
			if err := setConfigValue(gf.configPath, gf.profile, "models."+class, model); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s models.%s = %s\n", ui.IconOK, class, model)
			if err := convergeModelTier(cmd.Context(), out, p.Models, model); err != nil {
				return err
			}
		case "limits":
			window, err := tui.Select(out, in, "Which window?", []tui.Option{
				{Label: "daily_tokens", Value: "daily", Hint: fmt.Sprintf("%d", p.Limits.DailyTokens.Int())},
				{Label: "weekly_tokens", Value: "weekly", Hint: fmt.Sprintf("%d", p.Limits.WeeklyTokens.Int())},
			})
			if err != nil {
				return err
			}
			v, err := tui.Input(out, in, "Token budget", "1500000", "")
			if err != nil {
				return err
			}
			if err := setLimit(gf, window, v); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s limits updated\n", ui.IconOK)
		case "automations":
			opts := make([]tui.Option, 0, len(p.Automations)+1)
			for name, a := range p.Automations {
				state := "off"
				if a.Enabled {
					state = "on"
				}
				opts = append(opts, tui.Option{Label: name, Value: name, Hint: state})
			}
			if len(opts) == 0 {
				fmt.Fprintf(out, "%s no automations configured in this profile\n", ui.IconWarn)
				continue
			}
			name, err := tui.Select(out, in, "Toggle which automation?", opts)
			if err != nil {
				return err
			}
			enable := !p.Automations[name].Enabled
			if err := setAutomationEnabled(gf, name, enable); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s %s → %v\n", ui.IconOK, name, enable)
		case "dashboard":
			v, err := tui.Input(out, in, "Dashboard port", "7777", strconv.Itoa(p.Dashboard.Port))
			if err != nil {
				return err
			}
			if err := setConfigValue(gf.configPath, gf.profile, "dashboard.port", v); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s dashboard.port = %s\n", ui.IconOK, v)
		}
	}
}

// askModelForClass runs the provider-aware model prompt for one tier
// (ADR-015): pick a provider, then a model string where one is needed.
// synthesis is Claude-only; apple is offered for classify on macOS only.
func askModelForClass(out io.Writer, in io.Reader, p config.Profile, class string) (string, error) {
	if class == "synthesis" {
		return tui.Input(out, in, "Model string for synthesis", "claude-opus-4-8", currentModel(p, class))
	}
	providers := []tui.Option{
		{Label: "Claude", Value: "claude", Hint: "subscription/enterprise via claude -p (default)"},
		{Label: "Ollama (local)", Value: "ollama", Hint: "free + offline; needs a pulled model"},
	}
	if class == "classify" && runtime.GOOS == "darwin" {
		providers = append(providers, tui.Option{Label: "Apple on-device (local)", Value: "apple", Hint: "Foundation Models; zero install, classify only"})
	}
	provider, err := tui.Select(out, in, "Provider for "+class, providers)
	if err != nil {
		return "", err
	}
	switch provider {
	case "apple":
		return "apple", nil
	case "ollama":
		model, err := tui.Input(out, in, "Ollama model for "+class, "qwen3:8b", "")
		if err != nil {
			return "", err
		}
		return "ollama:" + model, nil
	default:
		return tui.Input(out, in, "Model string for "+class, "claude-sonnet-5", currentModel(p, class))
	}
}

func currentModel(p config.Profile, class string) string {
	switch class {
	case "classify":
		return p.Models.Classify
	case "routine":
		return p.Models.Routine
	default:
		return p.Models.Synthesis
	}
}

func newConfigureModelsCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "models <classify|routine|synthesis> <model>",
		Short: "Set the model for an operation class (Claude string, ollama:<model>, or apple)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			class := args[0]
			if class != "classify" && class != "routine" && class != "synthesis" {
				return fmt.Errorf("class must be classify, routine or synthesis (got %q)", class)
			}
			// setConfigValue re-validates the whole config, so the ADR-015
			// rules (no local synthesis, apple = classify only) reject here.
			if err := setConfigValue(gf.configPath, gf.profile, "models."+class, args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s models.%s = %s\n", ui.IconOK, class, args[1])

			// Converge local providers with the NEW config (probe ollama /
			// compile + probe the Apple helper).
			cfg, err := config.Load(gf.configPath)
			if err != nil {
				return err
			}
			_, profile, err := cfg.ResolveProfile(gf.profile)
			if err != nil {
				return err
			}
			return convergeModelTier(cmd.Context(), cmd.OutOrStdout(), profile.Models, args[1])
		},
	}
}

func newConfigureLimitsCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "limits <daily|weekly> <tokens>",
		Short: "Set a token budget window",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := setLimit(gf, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s limits.%s_tokens = %s\n", ui.IconOK, args[0], args[1])
			return nil
		},
	}
}

func setLimit(gf *globalFlags, window, value string) error {
	var key string
	switch window {
	case "daily":
		key = "limits.daily_tokens"
	case "weekly":
		key = "limits.weekly_tokens"
	default:
		return fmt.Errorf("window must be daily or weekly (got %q)", window)
	}
	if _, err := strconv.ParseInt(strings.ReplaceAll(value, "_", ""), 10, 64); err != nil {
		return fmt.Errorf("token budget must be a number (got %q)", value)
	}
	return setConfigValue(gf.configPath, gf.profile, key, value)
}

func newConfigureDashboardPortCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard-port <port>",
		Short: "Set the local dashboard port",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := setConfigValue(gf.configPath, gf.profile, "dashboard.port", args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s dashboard.port = %s\n", ui.IconOK, args[0])
			return nil
		},
	}
}

func newConfigureAutomationsCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "automations <name> <on|off>",
		Short: "Enable or disable an automation",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			var enable bool
			switch args[1] {
			case "on":
				enable = true
			case "off":
				enable = false
			default:
				return fmt.Errorf("state must be on or off (got %q)", args[1])
			}
			if err := setAutomationEnabled(gf, name, enable); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s automations.%s.enabled = %v\n", ui.IconOK, name, enable)
			return nil
		},
	}
}

// setAutomationEnabled toggles automations.<name>.enabled. Unlike the other
// keys, automation entries are partial overrides that often don't exist yet in
// the config, so a missing entry is CREATED by replacing the profile's
// automations node (comments inside that one node are regenerated; the rest of
// the file is untouched).
func setAutomationEnabled(gf *globalFlags, name string, enable bool) error {
	err := setConfigValue(gf.configPath, gf.profile, "automations."+name+".enabled", strconv.FormatBool(enable))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		return err
	}

	raw, rerr := os.ReadFile(gf.configPath)
	if rerr != nil {
		return rerr
	}
	cfg, perr := config.Parse(raw)
	if perr != nil {
		return perr
	}
	profileName := cfg.ResolveProfileName(gf.profile)

	// Merge the toggle into the existing (possibly empty) automations map.
	autos := map[string]map[string]any{}
	if p, ok := cfg.Profiles[profileName]; ok {
		for n, a := range p.Automations {
			entry := map[string]any{"enabled": a.Enabled}
			if a.Schedule != "" {
				entry["schedule"] = a.Schedule
			}
			if a.Model != "" {
				entry["model"] = a.Model
			}
			if a.BudgetTokens.Int() != 0 {
				entry["budget_tokens"] = a.BudgetTokens.Int()
			}
			if a.CatchUp != "" {
				entry["catch_up"] = a.CatchUp
			}
			if a.DryRun {
				entry["dry_run"] = true
			}
			autos[n] = entry
		}
	}
	if _, ok := autos[name]; !ok {
		autos[name] = map[string]any{}
	}
	autos[name]["enabled"] = enable

	rendered, merr := yaml.Marshal(autos)
	if merr != nil {
		return merr
	}
	file, ferr := parser.ParseBytes(raw, parser.ParseComments)
	if ferr != nil {
		return fmt.Errorf("parse config: %w", ferr)
	}
	path, perr2 := yaml.PathString(jsonPathFor(profileName, "automations"))
	if perr2 != nil {
		return perr2
	}
	if err := path.ReplaceWithReader(file, bytes.NewReader(rendered)); err != nil {
		return fmt.Errorf("set automations.%s: %w", name, err)
	}
	updated := []byte(file.String())
	if _, err := config.Parse(updated); err != nil {
		return fmt.Errorf("refusing to write: the change makes the config invalid: %w", err)
	}
	return writeFileAtomic(gf.configPath, updated)
}
