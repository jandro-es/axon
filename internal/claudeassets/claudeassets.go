// Package claudeassets generates the Claude Code integration files that wire a
// vault to AXON (init step 7, Component 08 §3-4): a profile-aware CLAUDE.md, a
// profile-scoped .mcp.json registering the AXON MCP server, a settings.json
// installing the deterministic hooks, and the plugin's skills + subagents. All
// writes are non-destructive (existing user files are never clobbered), so a
// re-init reports "already present".
package claudeassets

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/jandro-es/axon/internal/vault"
)

//go:embed assets
var assets embed.FS

// Params parameterise the generated files for a profile.
type Params struct {
	Profile    string // active profile name
	Binary     string // absolute path to the axon binary
	ConfigPath string // absolute path to axon.config.yaml
	ConfigDir  string // CLAUDE_CONFIG_DIR (profile-isolated auth)
	AxonHome   string // AXON_HOME
}

// Result reports what was created vs already present.
type Result struct {
	Created []string
	Skipped []string
}

// Changed reports whether anything was written.
func (r Result) Changed() bool { return len(r.Created) > 0 }

// staticAssets maps embedded files to their destination under .claude/. These
// are the plugin's skills and subagents (Component 08 §3); they are placed at
// the project level so Claude Code discovers them directly.
var staticAssets = []struct{ dest, asset string }{
	{".claude/agents/librarian.md", "assets/agents/librarian.md"},
	{".claude/agents/summariser.md", "assets/agents/summariser.md"},
	{".claude/agents/triager.md", "assets/agents/triager.md"},
	{".claude/skills/ingest-url/SKILL.md", "assets/skills/ingest-url.md"},
	{".claude/skills/run-daily-log/SKILL.md", "assets/skills/run-daily-log.md"},
	{".claude/skills/triage-inbox/SKILL.md", "assets/skills/triage-inbox.md"},
	{".claude/skills/suggest-links/SKILL.md", "assets/skills/suggest-links.md"},
	{".claude/skills/weekly-review/SKILL.md", "assets/skills/weekly-review.md"},
}

// Generate writes the .claude/ integration files into the vault, idempotently.
func Generate(v *vault.FS, p Params) (Result, error) {
	var res Result
	record := func(dest string, created bool) {
		if created {
			res.Created = append(res.Created, dest)
		} else {
			res.Skipped = append(res.Skipped, dest)
		}
	}

	// CLAUDE.md (templated, profile-aware).
	claudeMD, err := renderTemplate("assets/CLAUDE.md.tmpl", p)
	if err != nil {
		return res, err
	}
	if created, err := v.Create(".claude/CLAUDE.md", claudeMD); err != nil {
		return res, err
	} else {
		record(".claude/CLAUDE.md", created)
	}

	// .mcp.json (generated JSON; profile-scoped).
	if created, err := v.Create(".claude/.mcp.json", p.mcpJSON()); err != nil {
		return res, err
	} else {
		record(".claude/.mcp.json", created)
	}

	// settings.json (hooks).
	if created, err := v.Create(".claude/settings.json", p.settingsJSON()); err != nil {
		return res, err
	} else {
		record(".claude/settings.json", created)
	}

	// Plugin manifest.
	if created, err := v.Create(".claude/plugins/axon/.claude-plugin/plugin.json", pluginManifest()); err != nil {
		return res, err
	} else {
		record(".claude/plugins/axon/.claude-plugin/plugin.json", created)
	}

	// Skills + subagents (static).
	for _, a := range staticAssets {
		content, err := assets.ReadFile(a.asset)
		if err != nil {
			return res, fmt.Errorf("read asset %q: %w", a.asset, err)
		}
		created, err := v.Create(a.dest, string(content))
		if err != nil {
			return res, err
		}
		record(a.dest, created)
	}

	sort.Strings(res.Created)
	sort.Strings(res.Skipped)
	return res, nil
}

func renderTemplate(asset string, p Params) (string, error) {
	raw, err := assets.ReadFile(asset)
	if err != nil {
		return "", fmt.Errorf("read template %q: %w", asset, err)
	}
	tmpl, err := template.New(asset).Parse(string(raw))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, p); err != nil {
		return "", err
	}
	return b.String(), nil
}

// mcpJSON registers the AXON MCP server, launched as `axon mcp` scoped to this
// profile, with the profile's CLAUDE_CONFIG_DIR so it uses the right account.
func (p Params) mcpJSON() string {
	type server struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	doc := map[string]any{
		"mcpServers": map[string]any{
			"axon": server{
				Command: p.Binary,
				Args:    []string{"mcp", "--config", p.ConfigPath, "--profile", p.Profile},
				Env:     p.env(),
			},
		},
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return string(b) + "\n"
}

// settingsJSON installs the four deterministic hooks, each a thin `axon hook`
// call. PreToolUse is scoped to file-mutating + Bash tools; PostToolUse to AXON
// MCP tools.
func (p Params) settingsJSON() string {
	type hookCmd struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type matcherGroup struct {
		Matcher string    `json:"matcher,omitempty"`
		Hooks   []hookCmd `json:"hooks"`
	}
	mk := func(event string) []matcherGroup {
		return []matcherGroup{{Hooks: []hookCmd{{Type: "command", Command: p.hookCommand(event)}}}}
	}
	pre := []matcherGroup{{
		Matcher: "Write|Edit|MultiEdit|NotebookEdit|Bash",
		Hooks:   []hookCmd{{Type: "command", Command: p.hookCommand("PreToolUse")}},
	}}
	post := []matcherGroup{{
		Matcher: "mcp__axon__.*",
		Hooks:   []hookCmd{{Type: "command", Command: p.hookCommand("PostToolUse")}},
	}}
	doc := map[string]any{
		"hooks": map[string]any{
			"SessionStart": mk("SessionStart"),
			"PreToolUse":   pre,
			"PostToolUse":  post,
			"Stop":         mk("Stop"),
		},
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return string(b) + "\n"
}

func (p Params) hookCommand(event string) string {
	return fmt.Sprintf("%q hook %s --config %q --profile %q", p.Binary, event, p.ConfigPath, p.Profile)
}

func (p Params) env() map[string]string {
	env := map[string]string{}
	if p.ConfigDir != "" {
		env["CLAUDE_CONFIG_DIR"] = p.ConfigDir
	}
	if p.AxonHome != "" {
		env["AXON_HOME"] = p.AxonHome
	}
	return env
}

func pluginManifest() string {
	doc := map[string]any{
		"name":        "axon",
		"version":     "0.5.0",
		"description": "AXON second-brain tools: wikilink-safe vault ops, knowledge ingestion, hybrid search, token-aware automations.",
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return string(b) + "\n"
}
