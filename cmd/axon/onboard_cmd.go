package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/claudeassets"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/identity"
)

// onboardFile is the YAML/JSON shape accepted by `--from`, mapped onto
// identity.Values for unattended onboarding.
type onboardFile struct {
	Name          string   `yaml:"name"`
	Role          string   `yaml:"role"`
	Timezone      string   `yaml:"timezone"`
	Communication string   `yaml:"communication"`
	Goals         []string `yaml:"goals"`
	People        []string `yaml:"people"`
	Projects      []string `yaml:"projects"`
	Tools         []string `yaml:"tools"`
	AgentName     string   `yaml:"agent_name"`
	Tone          string   `yaml:"tone"`
	Boundaries    []string `yaml:"boundaries"`
}

func (f onboardFile) toValues() identity.Values {
	return identity.Values{
		Name: f.Name, Role: f.Role, Timezone: f.Timezone, Communication: f.Communication,
		Goals: f.Goals, People: f.People, Projects: f.Projects, Tools: f.Tools,
		AgentName: f.AgentName, Tone: f.Tone, Boundaries: f.Boundaries,
	}
}

// onboardReport is the machine-readable (--json) result, secret-free.
type onboardReport struct {
	Profile    string      `json:"profile"`
	Mode       string      `json:"mode"` // first-run | update
	Values     onboardFile `json:"values"`
	Created    []string    `json:"created"`
	Skipped    []string    `json:"skipped"`
	ClaudeCode []string    `json:"claude_code_wired"`
	OK         bool        `json:"ok"`
}

func newOnboardCmd(gf *globalFlags) *cobra.Command {
	var asJSON, nonInteractive bool
	var fromPath string
	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "Teach AXON who you are: create the personal identity & memory layer",
		Long: "Interactively populate the personal identity & memory layer under\n" +
			"02-Areas/Profile/ (USER.md, SOUL.md, MEMORY.md) so the assistant knows you\n" +
			"in every session. Idempotent and non-destructive: existing files are kept,\n" +
			"never overwritten. Makes NO model call — it is an interview, not a\n" +
			"generation. Re-run any time; edit the files directly in Obsidian.\n\n" +
			"Flags: --non-interactive (use defaults/--from, no prompts), --from <file>\n" +
			"(YAML/JSON answers), --json (emit the resulting profile, secret-free).",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, err := loadProfileDeps(gf, false) // identity layer needs the vault only
			if err != nil {
				return err
			}
			defer deps.close()

			out := cmd.OutOrStdout()
			present := identity.Present(deps.vault)
			mode := "first-run"
			if present {
				mode = "update"
			}

			// Gather answers: from a file, from prompts, or defaults.
			vals, err := gatherOnboardValues(cmd, fromPath, nonInteractive, asJSON, present)
			if err != nil {
				return err
			}

			res, err := identity.Generate(deps.vault, vals)
			if err != nil {
				return err
			}

			// Wire Claude Code (.claude/) idempotently — the same files `axon init`
			// writes, so onboarding doubles as client setup (Component 12 §2 step 4).
			wiring, werr := ensureClaudeWiring(deps, gf)

			rep := onboardReport{
				Profile: deps.name, Mode: mode, Values: valuesToFile(vals),
				Created: res.Created, Skipped: res.Skipped, ClaudeCode: wiring.Created, OK: true,
			}
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}

			// Human summary.
			fmt.Fprintf(out, "axon onboard — profile %q (%s)\n", deps.name, mode)
			fmt.Fprintln(out, strings.Repeat("─", 60))
			for _, p := range res.Created {
				fmt.Fprintf(out, "  ✓ created  %s\n", p)
			}
			for _, p := range res.Skipped {
				fmt.Fprintf(out, "  ↻ kept     %s (existing edits preserved)\n", p)
			}
			if werr != nil {
				fmt.Fprintf(out, "  ⚠ claude wiring: %v\n", werr)
			} else if len(wiring.Created) > 0 {
				fmt.Fprintf(out, "  ✓ wired Claude Code (.claude/: %d file(s))\n", len(wiring.Created))
			} else {
				fmt.Fprintln(out, "  ↻ Claude Code wiring already present")
			}
			fmt.Fprintln(out, strings.Repeat("─", 60))
			fmt.Fprintf(out, "Your profile lives in %s/ — edit it any time in Obsidian.\n", identity.Dir)
			fmt.Fprintln(out, "Other clients: `axon mcp install --client desktop` wires Claude Desktop.")
			fmt.Fprintln(out, "Next: open Claude Code in the vault — it now greets you with your profile.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the resulting profile as JSON (secret-free); implies non-interactive")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "do not prompt; use --from and/or defaults")
	cmd.Flags().StringVar(&fromPath, "from", "", "read answers from a YAML/JSON file instead of prompting")
	return cmd
}

// gatherOnboardValues resolves the interview answers from --from, interactive
// prompts, or defaults, in that order of precedence per field.
func gatherOnboardValues(cmd *cobra.Command, fromPath string, nonInteractive, asJSON, present bool) (identity.Values, error) {
	var vals identity.Values
	if fromPath != "" {
		raw, err := os.ReadFile(fromPath)
		if err != nil {
			return vals, fmt.Errorf("read --from %q: %w", fromPath, err)
		}
		var f onboardFile
		if err := yaml.Unmarshal(raw, &f); err != nil {
			return vals, fmt.Errorf("parse --from %q: %w", fromPath, err)
		}
		vals = f.toValues()
	}

	// --json or --non-interactive: take what we have (file/defaults), no prompts.
	if asJSON || nonInteractive {
		return vals, nil
	}

	out := cmd.OutOrStdout()
	r := bufio.NewReader(cmd.InOrStdin())
	if present {
		fmt.Fprintf(out, "An identity layer already exists in %s/. Existing files are kept;\n", identity.Dir)
		fmt.Fprintln(out, "answers below only fill files that are still missing. Press Enter to skip.")
	} else {
		fmt.Fprintln(out, "Let's set up your profile. Press Enter to accept the default / skip.")
	}
	fmt.Fprintln(out)

	vals.Name = ask(out, r, "Your name", vals.Name)
	vals.Role = ask(out, r, "Your role / what you do", vals.Role)
	vals.Timezone = ask(out, r, "Timezone (e.g. Europe/Madrid)", vals.Timezone)
	vals.Communication = ask(out, r, "Preferred communication style", orDefault(vals.Communication, "concise, no preamble; bullet points"))
	vals.Goals = askList(out, r, "Current goals (comma-separated)", vals.Goals)
	vals.People = askList(out, r, "Key people (comma-separated)", vals.People)
	vals.Projects = askList(out, r, "Active projects (comma-separated)", vals.Projects)
	vals.Tools = askList(out, r, "Tools you rely on (comma-separated)", vals.Tools)
	fmt.Fprintln(out)
	vals.AgentName = ask(out, r, "Name for your assistant", orDefault(vals.AgentName, "Axon"))
	vals.Tone = ask(out, r, "Assistant tone", orDefault(vals.Tone, "direct, warm, pragmatic"))
	vals.Boundaries = askList(out, r, "Boundaries the assistant must respect (comma-separated)", vals.Boundaries)
	fmt.Fprintln(out)
	return vals, nil
}

func ask(w io.Writer, r *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(w, "%s: ", label)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func askList(w io.Writer, r *bufio.Reader, label string, def []string) []string {
	defStr := strings.Join(def, ", ")
	answer := ask(w, r, label, defStr)
	if strings.TrimSpace(answer) == "" {
		return def
	}
	return splitList(answer)
}

func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func valuesToFile(v identity.Values) onboardFile {
	return onboardFile{
		Name: v.Name, Role: v.Role, Timezone: v.Timezone, Communication: v.Communication,
		Goals: v.Goals, People: v.People, Projects: v.Projects, Tools: v.Tools,
		AgentName: v.AgentName, Tone: v.Tone, Boundaries: v.Boundaries,
	}
}

// ensureClaudeWiring (re)generates the .claude/ integration idempotently, the
// same wiring `axon init` produces. Non-destructive — existing files are kept.
func ensureClaudeWiring(deps *profileDeps, gf *globalFlags) (claudeassets.Result, error) {
	absCfg, err := filepath.Abs(gf.configPath)
	if err != nil {
		absCfg = gf.configPath
	}
	binary, _ := os.Executable()
	if binary == "" {
		binary = "axon"
	}
	return claudeassets.Generate(deps.vault, claudeassets.Params{
		Profile:    deps.name,
		Binary:     binary,
		ConfigPath: absCfg,
		ConfigDir:  deps.paths.ConfigDir,
		AxonHome:   config.AxonHome(),
	})
}
