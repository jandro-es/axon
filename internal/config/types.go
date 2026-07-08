// Package config defines AXON's declarative configuration surface: the typed
// schema for config.yaml (read from ~/.axon/config.yaml by default), profile
// resolution, secret references, path expansion and content hashing. Every other
// package depends on this one; it depends on nothing internal (see the
// dependency rule in docs/02).
package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Config is the root of config.yaml. One installation runs ONE active
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
	// Ingestion tunes URL fetching beyond the egress policy — per-domain auth
	// headers for sources behind SSO (Confluence, internal wikis). Optional.
	Ingestion IngestionConfig `yaml:"ingestion"`
	// Resurfacing tunes the R9 spaced-repetition review scheduler (FR-151…153).
	// Optional: absent → the Go defaults via the accessors below.
	Resurfacing ResurfacingConfig `yaml:"resurfacing"`
	// Interop wires optional external/community MCP backends (FR-54). Optional.
	Interop InteropConfig `yaml:"interop"`
	// Capture tunes the capture automation (ADR-016). Optional: an absent block
	// resolves to heuristic enrichment and the default archive folder.
	Capture CaptureConfig `yaml:"capture"`
	// Subscriptions declares the RSS/Atom feeds AXON polls (ADR-019).
	// Optional: an absent block means no feeds and the automation skips.
	Subscriptions SubscriptionsConfig `yaml:"subscriptions"`
}

// SubscriptionsConfig declares the RSS/Atom feeds AXON polls (ADR-019).
type SubscriptionsConfig struct {
	// Enrich selects metadata enrichment for ingested items: "heuristic"
	// (default, zero tokens) or "claude" (chokepoint, routine tier).
	Enrich string `yaml:"enrich,omitempty"`
	// MaxPerTick caps new items ingested per feed per tick (default 5).
	MaxPerTick int `yaml:"max_per_tick,omitempty"`
	// Feeds are the subscribed feed URLs.
	Feeds []Feed `yaml:"feeds,omitempty"`
}

// Feed is one subscribed feed.
type Feed struct {
	URL string `yaml:"url"`
}

// EnrichMode returns the enrichment mode, defaulting to "heuristic".
func (c SubscriptionsConfig) EnrichMode() string {
	if c.Enrich == "" {
		return "heuristic"
	}
	return c.Enrich
}

// PerTick returns the per-feed per-tick ingestion cap, defaulting to 5.
func (c SubscriptionsConfig) PerTick() int {
	if c.MaxPerTick <= 0 {
		return 5
	}
	return c.MaxPerTick
}

// validateSubscriptions applies the subscriptions rules (ADR-019).
func validateSubscriptions(c SubscriptionsConfig) error {
	if c.Enrich != "" && c.Enrich != "heuristic" && c.Enrich != "claude" {
		return fmt.Errorf("subscriptions.enrich must be heuristic or claude (got %q)", c.Enrich)
	}
	if c.MaxPerTick < 0 {
		return fmt.Errorf("subscriptions.max_per_tick must be >= 0 (got %d)", c.MaxPerTick)
	}
	for _, f := range c.Feeds {
		u, err := url.Parse(f.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("subscriptions feed %q must be an http(s) URL", f.URL)
		}
	}
	return nil
}

// CaptureConfig tunes the capture automation (ADR-016): the FR-26 funnel that
// ingests own-line URLs from Inbox notes and files dropped into 00-Inbox.
type CaptureConfig struct {
	// Enrich selects metadata enrichment for captured items: "heuristic"
	// (default, zero tokens) or "claude" (through the token-manager
	// chokepoint on the routine tier).
	Enrich string `yaml:"enrich,omitempty"`
	// ArchiveDir is the vault-relative folder for ingested inbox originals.
	// Default: 04-Archive/Capture.
	ArchiveDir string `yaml:"archive_dir,omitempty"`
}

// EnrichMode returns the enrichment mode, defaulting to "heuristic".
func (c CaptureConfig) EnrichMode() string {
	if c.Enrich == "" {
		return "heuristic"
	}
	return c.Enrich
}

// Archive returns the archive folder, defaulting to 04-Archive/Capture.
func (c CaptureConfig) Archive() string {
	if c.ArchiveDir == "" {
		return "04-Archive/Capture"
	}
	return c.ArchiveDir
}

// validateCapture applies the capture-block rules struct tags can't express.
func validateCapture(c CaptureConfig) error {
	if c.Enrich != "" && c.Enrich != "heuristic" && c.Enrich != "claude" {
		return fmt.Errorf("capture.enrich must be heuristic or claude (got %q)", c.Enrich)
	}
	if c.ArchiveDir != "" {
		if strings.HasPrefix(c.ArchiveDir, "/") || strings.Contains(c.ArchiveDir, "..") {
			return fmt.Errorf("capture.archive_dir must be a vault-relative path (got %q)", c.ArchiveDir)
		}
	}
	return nil
}

// IngestionConfig configures URL-fetch behaviour for the ingestion pipeline.
type IngestionConfig struct {
	// Auth attaches a header to fetches whose host matches an entry's domain
	// (or a subdomain of it) — and NEVER to any other host, including redirect
	// targets. This is how pages behind SSO (Confluence, internal wikis) become
	// ingestable: a PAT/API token in a header, not a browser session.
	Auth []IngestAuth `yaml:"auth" validate:"dive"`
	// OCR selects the scanned-PDF OCR provider used as a fallback when a PDF's
	// text layer is empty: "" or "off" (default), "apple" (on-device Vision via
	// a compiled Swift helper, macOS only), or "tesseract" (pdftoppm+tesseract).
	OCR string `yaml:"ocr" validate:"omitempty,oneof=off apple tesseract"`
	// OCRHelper overrides the compiled Apple OCR helper path (apple provider).
	OCRHelper string `yaml:"ocr_helper"`
}

// OCRMode returns the configured OCR provider, defaulting to "off" when unset.
func (c IngestionConfig) OCRMode() string {
	if c.OCR == "" {
		return "off"
	}
	return c.OCR
}

// IngestAuth is one per-domain credential for ingestion fetches. Value may be
// a secret reference (env:VAR, keychain:...) resolved at fetch time; it is
// never logged or persisted in events.
type IngestAuth struct {
	Domain string `yaml:"domain" validate:"required"`
	// Header defaults to "Authorization" when empty.
	Header string `yaml:"header"`
	Value  string `yaml:"value" validate:"required"`
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
	// CaptureSessions gates the Stop-hook session recorder (ADR-021,
	// NFR-14). Pointer so absence means ON; a stricter profile sets false.
	CaptureSessions *bool `yaml:"capture_sessions"`
}

// InjectEnabled reports whether SessionStart identity injection is on (default
// true when the key is absent).
func (m MemoryConfig) InjectEnabled() bool { return m.Inject == nil || *m.Inject }

// SessionCaptureEnabled reports whether finished sessions are recorded for
// memory distillation (default true; capture_sessions: false disables).
func (m MemoryConfig) SessionCaptureEnabled() bool {
	return m.CaptureSessions == nil || *m.CaptureSessions
}

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
	// AskEnabled gates the browser-triggered ask endpoint (ADR-023). Pointer
	// default-ON: unset = enabled; set false to forbid dashboard token spend.
	AskEnabled *bool `yaml:"ask_enabled,omitempty"`
	// CaptureEnabled gates the browser capture endpoint (ADR-024). Pointer
	// default-ON: unset = enabled; set false to forbid browser vault writes.
	CaptureEnabled *bool `yaml:"capture_enabled,omitempty"`
	// RelatedEnabled gates the read-only related-notes endpoint (R8/FR-150).
	// Pointer default-ON: unset = enabled; set false to forbid the endpoint.
	RelatedEnabled *bool `yaml:"related_enabled,omitempty"`
}

// AskAllowed reports whether the dashboard Ask endpoint is enabled (default true).
func (d DashboardConfig) AskAllowed() bool { return d.AskEnabled == nil || *d.AskEnabled }

// CaptureAllowed reports whether the browser capture endpoint is enabled (default true).
func (d DashboardConfig) CaptureAllowed() bool { return d.CaptureEnabled == nil || *d.CaptureEnabled }

// RelatedAllowed reports whether the dashboard related-notes endpoint is enabled (default true).
func (d DashboardConfig) RelatedAllowed() bool { return d.RelatedEnabled == nil || *d.RelatedEnabled }

// EmbeddingsConfig configures the local embedding provider. dim MUST match the
// model's output dimension; changing the model or provider forces a full
// re-index (`axon reindex --embeddings`).
type EmbeddingsConfig struct {
	Provider  string `yaml:"provider" validate:"required,oneof=ollama apple"`
	Host      string `yaml:"host"` // ollama only
	Model     string `yaml:"model" validate:"required"`
	Dim       int    `yaml:"dim" validate:"required,min=1"`
	BatchSize int    `yaml:"batch_size" validate:"required,min=1"`
	// Helper overrides the apple provider's helper binary path.
	// Default: DefaultAppleHelperPath(). Ignored by other providers.
	Helper string `yaml:"helper,omitempty"`
}

// ModelsConfig names the preferred model per operation class. Claude strings
// are passed to `claude -p --model`; "ollama:<model>" and "apple" route the
// tier to a local provider through the same token-manager chokepoint
// (ADR-015). synthesis is always Claude (validated).
type ModelsConfig struct {
	Classify  string `yaml:"classify" validate:"required"`
	Routine   string `yaml:"routine" validate:"required"`
	Synthesis string `yaml:"synthesis" validate:"required"`
	// OllamaHost is the Ollama server for local chat tiers (default
	// http://localhost:11434). Independent of embeddings.host.
	OllamaHost string `yaml:"ollama_host,omitempty"`
	// LocalFallback governs local-provider failures: "claude" (default)
	// falls forward through the normal budget path; "fail" surfaces the error.
	LocalFallback string `yaml:"local_fallback,omitempty"`
	// EvalMinPass gates local-tier promotion (R5.2/FR-142): a local
	// classify/routine model serves its tier only when its latest `axon eval`
	// pass rate is >= this percent. 0 (default) disables the gate — local tiers
	// route as configured. New installs scaffold 80; doctor nudges.
	EvalMinPass int `yaml:"eval_min_pass,omitempty" validate:"omitempty,min=0,max=100"`
	// Verify, set to "ollama:<model>", enables per-call verification of local
	// routine answers (R5.3/FR-144): after a successful local routine response a
	// cheap local judge scores it 0–10; a score below VerifyMinScore escalates
	// the call to Claude. "" or "off" disables (default). Only the routine tier
	// is verified — synthesis is always Claude, classify is deterministically
	// validated.
	Verify string `yaml:"verify,omitempty"`
	// VerifyMinScore is the 0–10 confidence floor below which a verified local
	// routine answer escalates to Claude. 0 (unset) → default 6. Ignored when
	// verify is off.
	VerifyMinScore int `yaml:"verify_min_score,omitempty" validate:"omitempty,min=0,max=10"`
	// AppleHelper overrides the Foundation Models helper binary path.
	// Default: DefaultAppleLMHelperPath(). Ignored unless a tier is "apple".
	AppleHelper string `yaml:"apple_helper,omitempty"`
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
	// Index selects the vector-search backend (ADR-025): "brute" (exact full
	// scan, the default) or "ann" (IVF-flat approximate, opt-in).
	Index string    `yaml:"index,omitempty" validate:"omitempty,oneof=brute ann"`
	ANN   ANNConfig `yaml:"ann,omitempty"`
	// Rerank selects an optional local reranker applied to a wider candidate
	// pool: "" or "off" (default), or "ollama:<model>" (ADR-027). Best-effort —
	// any failure falls back to the fused order.
	Rerank string `yaml:"rerank,omitempty"`
	// RerankOverfetch is the candidate multiple fetched before reranking
	// (default 3; ignored when rerank is off).
	RerankOverfetch int `yaml:"rerank_overfetch,omitempty"`
}

// RerankMode returns the configured reranker, defaulting to "off" when unset.
func (r RetrievalConfig) RerankMode() string {
	if r.Rerank == "" {
		return "off"
	}
	return r.Rerank
}

// RerankOverfetchOr returns the candidate multiple for reranking, default 3.
func (r RetrievalConfig) RerankOverfetchOr() int {
	if r.RerankOverfetch <= 0 {
		return 3
	}
	return r.RerankOverfetch
}

// ANNConfig tunes the IVF-flat index (ADR-025). Zero values take documented
// defaults via the accessors below.
type ANNConfig struct {
	Threshold int `yaml:"threshold,omitempty"` // min vectors before ann engages
	NProbe    int `yaml:"nprobe,omitempty"`    // clusters probed per query
}

// ResurfacingConfig tunes the resurfacer's spaced-repetition schedule and the
// opt-in contradiction check (R9). Zero values take the documented defaults.
type ResurfacingConfig struct {
	// IntervalsWeeks is the spaced-repetition ladder in weeks (rung 0..N; the
	// last rung is the leech cap). Empty → [1,2,4,8,16].
	IntervalsWeeks []int `yaml:"intervals_weeks,omitempty" validate:"omitempty,dive,gt=0"`
	// ContradictionMaxChecks caps model calls per run for note-contradiction
	// detection. 0 → default 3. Set explicitly to control spend; the path is
	// still gated on the resurfacer having budget_tokens > 0.
	ContradictionMaxChecks int `yaml:"contradiction_max_checks,omitempty" validate:"omitempty,gte=0"`
}

// IntervalsWeeksOr returns the configured ladder or the default [1,2,4,8,16].
func (r ResurfacingConfig) IntervalsWeeksOr() []int {
	if len(r.IntervalsWeeks) == 0 {
		return []int{1, 2, 4, 8, 16}
	}
	return r.IntervalsWeeks
}

// ContradictionMaxChecksOr returns the per-run model-call cap, default 3.
func (r ResurfacingConfig) ContradictionMaxChecksOr() int {
	if r.ContradictionMaxChecks <= 0 {
		return 3
	}
	return r.ContradictionMaxChecks
}

// IndexMode returns the configured vector backend, defaulting to "brute".
func (r RetrievalConfig) IndexMode() string {
	if r.Index == "" {
		return "brute"
	}
	return r.Index
}

// ThresholdOr returns the ann engage-threshold, defaulting to 10000 vectors.
func (a ANNConfig) ThresholdOr() int {
	if a.Threshold <= 0 {
		return 10000
	}
	return a.Threshold
}

// NProbeOr returns clusters probed per query, defaulting to 8.
func (a ANNConfig) NProbeOr() int {
	if a.NProbe <= 0 {
		return 8
	}
	return a.NProbe
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
	// Agentic opts a tool-using automation in/out of its agentic path
	// (ADR-017). nil = the automation's own default; false = one-shot.
	Agentic *bool `yaml:"agentic,omitempty"`
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
