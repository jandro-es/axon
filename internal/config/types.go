// Package config defines AXON's declarative configuration surface: the typed
// schema for axon.config.yaml, profile resolution, secret references, path
// expansion and content hashing. Every other package depends on this one; it
// depends on nothing internal (see the dependency rule in docs/02).
package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Config is the root of axon.config.yaml. One installation runs ONE active
// profile, but the profiles map keeps both templates under version control.
type Config struct {
	Version       int                `yaml:"version" validate:"required,eq=1"`
	ProjectName   string             `yaml:"project_name" validate:"required"`
	ActiveProfile string             `yaml:"active_profile" validate:"required"`
	Profiles      map[string]Profile `yaml:"profiles" validate:"required,min=1,dive"`

	// Prices is only consulted in auth_mode: api_key to compute cost_usd. Kept
	// out of code so it updates without a release. Optional everywhere else.
	Prices map[string]Price `yaml:"prices,omitempty" validate:"dive"`
}

// Profile is the unit of isolation: its own vault, data dir, Claude account,
// policy block and automation set. Nothing is shared across profiles.
type Profile struct {
	VaultPath   string                `yaml:"vault_path" validate:"required"`
	DataDir     string                `yaml:"data_dir" validate:"required"`
	Claude      ClaudeConfig          `yaml:"claude" validate:"required"`
	Dashboard   DashboardConfig       `yaml:"dashboard" validate:"required"`
	Embeddings  EmbeddingsConfig      `yaml:"embeddings" validate:"required"`
	Models      ModelsConfig          `yaml:"models" validate:"required"`
	Limits      LimitsConfig          `yaml:"limits" validate:"required"`
	Retrieval   RetrievalConfig       `yaml:"retrieval" validate:"required"`
	Policy      PolicyConfig          `yaml:"policy" validate:"required"`
	Automations map[string]Automation `yaml:"automations" validate:"dive"`
	// Memory governs the personal identity/memory layer (Component 12). Optional:
	// an absent block resolves to sensible defaults so existing configs keep
	// working unchanged.
	Memory MemoryConfig `yaml:"memory"`
	// Interop wires optional external/community MCP backends (FR-54). Optional.
	Interop InteropConfig `yaml:"interop"`
}

// InteropConfig configures optional third-party MCP servers AXON can register
// alongside its own when wiring a client (FR-54, ADR-005). AXON's server stays
// the default vault contract; these are alternatives the user opts into.
type InteropConfig struct {
	// ObsidianMCP is a community Obsidian MCP server offered as an alternative
	// vault backend behind the same tool contract.
	ObsidianMCP ExternalMCPServer `yaml:"obsidian_mcp"`
}

// ExternalMCPServer is a launch spec for a third-party stdio MCP server.
type ExternalMCPServer struct {
	Enabled bool              `yaml:"enabled"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
}

// Configured reports whether the external server is enabled and has a command.
func (e ExternalMCPServer) Configured() bool {
	return e.Enabled && e.Command != ""
}

// MemoryConfig governs the personal identity & memory layer injected at
// SessionStart (FR-70…FR-73, NFR-14). All fields are optional; the accessors
// below apply defaults so a profile may omit the `memory:` block entirely.
type MemoryConfig struct {
	// Inject toggles the deterministic SessionStart identity injection. A pointer
	// so an absent key defaults to ON (personal profile) while an explicit
	// `inject: false` (stricter work environment) is honoured (NFR-14).
	Inject *bool `yaml:"inject"`
	// SessionTokens bounds the injected block (default 1500). The injection never
	// makes a model call; this only caps how much of the layer is rendered.
	SessionTokens int `yaml:"session_tokens"`
	// RecentEntries is how many newest MEMORY.md entries to inject (default 10).
	RecentEntries int `yaml:"recent_entries"`
}

// InjectEnabled reports whether SessionStart identity injection is on (default
// true when the key is absent).
func (m MemoryConfig) InjectEnabled() bool { return m.Inject == nil || *m.Inject }

// SessionTokenBudget returns the injection token ceiling, defaulting to 1500.
func (m MemoryConfig) SessionTokenBudget() int {
	if m.SessionTokens > 0 {
		return m.SessionTokens
	}
	return 1500
}

// RecentMemoryEntries returns how many newest MEMORY entries to inject,
// defaulting to 10.
func (m MemoryConfig) RecentMemoryEntries() int {
	if m.RecentEntries > 0 {
		return m.RecentEntries
	}
	return 10
}

// ClaudeConfig selects how AXON reaches Claude. The default modes go through
// Claude Code (no API key); api_key is the only direct-API path.
type ClaudeConfig struct {
	AuthMode  string `yaml:"auth_mode" validate:"required,oneof=subscription enterprise api_key"`
	ConfigDir string `yaml:"config_dir"`
	// OAuthToken is a secret reference (env:NAME / keychain:NAME), not the
	// secret itself. Resolved at runtime via ResolveSecret. May be empty.
	OAuthToken string `yaml:"oauth_token"`
}

// DashboardConfig binds the local observability server. Localhost only.
type DashboardConfig struct {
	Host string `yaml:"host" validate:"required"`
	Port int    `yaml:"port" validate:"required,min=1,max=65535"`
}

// EmbeddingsConfig configures the local embedding provider. dim MUST match the
// model's output dimension; changing the model forces a full re-index.
type EmbeddingsConfig struct {
	Provider  string `yaml:"provider" validate:"required"`
	Host      string `yaml:"host"`
	Model     string `yaml:"model" validate:"required"`
	Dim       int    `yaml:"dim" validate:"required,min=1"`
	BatchSize int    `yaml:"batch_size" validate:"required,min=1"`
}

// ModelsConfig names the preferred Claude model per operation class. These are
// passed to `claude -p --model`; actual availability follows the plan tier.
type ModelsConfig struct {
	Classify  string `yaml:"classify" validate:"required"`
	Routine   string `yaml:"routine" validate:"required"`
	Synthesis string `yaml:"synthesis" validate:"required"`
}

// LimitsConfig is the token-awareness budget. On subscription/enterprise these
// guard rate-limit / Agent-SDK-credit burn, not dollars.
type LimitsConfig struct {
	DailyTokens     FlexInt `yaml:"daily_tokens" validate:"required"`
	WeeklyTokens    FlexInt `yaml:"weekly_tokens" validate:"required"`
	GuardPauseAtPct int     `yaml:"guard_pause_at_pct" validate:"min=0,max=100"`
	// DailyCostUSD applies only to auth_mode: api_key; ignored otherwise.
	DailyCostUSD float64 `yaml:"daily_cost_usd,omitempty"`
}

// RetrievalConfig bounds retrieved context assembled per Claude call.
type RetrievalConfig struct {
	TopK             int     `yaml:"top_k" validate:"required,min=1"`
	MaxContextTokens FlexInt `yaml:"max_context_tokens" validate:"required"`
}

// PolicyConfig is the per-profile safety envelope, enforced in code (not by
// asking the model). Work profiles default restrictive.
type PolicyConfig struct {
	DataResidency      string   `yaml:"data_residency"`
	EgressAllowlist    []string `yaml:"egress_allowlist"`
	IngestDomainsAllow []string `yaml:"ingest_domains_allow"`
	IngestDomainsDeny  []string `yaml:"ingest_domains_deny"`
	RedactionRules     []string `yaml:"redaction_rules"`
	// AllowedAutomations is an allow-list that overrides per-automation
	// enabled:true ("*" = all permitted).
	AllowedAutomations []string `yaml:"allowed_automations"`
}

// Automation entries are partial overrides: the work profile supplies only the
// fields it changes (e.g. {enabled: false}), so no sub-field may be required.
type Automation struct {
	Enabled      bool    `yaml:"enabled"`
	Schedule     string  `yaml:"schedule"`
	Model        string  `yaml:"model"`
	BudgetTokens FlexInt `yaml:"budget_tokens"`
	CatchUp      string  `yaml:"catch_up,omitempty"`
	DryRun       bool    `yaml:"dry_run,omitempty"`
}

// Price is a per-model unit price table entry (api_key mode only).
type Price struct {
	Input     float64 `yaml:"input"`
	Output    float64 `yaml:"output"`
	CacheRead float64 `yaml:"cache_read"`
}

// FlexInt is an int64 that accepts both plain YAML integers and human-grouped
// integers with underscore separators (e.g. 1_500_000). The example config
// uses underscores throughout, and not every YAML parser honours YAML 1.1
// underscore integers, so we normalise defensively at unmarshal time.
type FlexInt int64

// Int returns the value as a plain int64.
func (f FlexInt) Int() int64 { return int64(f) }

// UnmarshalYAML implements goccy/go-yaml's BytesUnmarshaler: it receives the
// raw scalar bytes, strips quotes/underscores, and parses a base-10 int64.
func (f *FlexInt) UnmarshalYAML(b []byte) error {
	s := strings.TrimSpace(string(b))
	s = strings.Trim(s, `"'`)
	s = strings.ReplaceAll(s, "_", "")
	switch s {
	case "", "~", "null":
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid integer %q: %w", string(b), err)
	}
	*f = FlexInt(n)
	return nil
}
