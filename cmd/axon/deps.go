package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/ingestion"
	"github.com/jandro-es/axon/internal/mcp"
	"github.com/jandro-es/axon/internal/rerank"
	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

// profileDeps bundles the per-profile runtime objects a data command needs.
type profileDeps struct {
	cfg        *config.Config
	name       string
	profile    config.Profile
	paths      config.ResolvedPaths
	db         *sql.DB
	vault      *vault.FS
	embedder   embeddings.Provider
	configPath string // absolute config path, for subprocess re-invocation (agentic MCP)
}

// loadProfileDeps loads + validates config, resolves the active profile, builds
// the vault and embedding provider, and (if openDB) opens + migrates the
// database. The caller must Close the db when done.
func loadProfileDeps(gf *globalFlags, openDB bool) (*profileDeps, error) {
	_ = config.LoadDotEnv(gf.envPath)
	cfg, err := config.Load(gf.configPath)
	if err != nil {
		return nil, err
	}
	name, profile, err := cfg.ResolveProfile(gf.profile)
	if err != nil {
		return nil, err
	}
	paths := profile.Paths()
	absCfg, aerr := filepath.Abs(gf.configPath)
	if aerr != nil {
		absCfg = gf.configPath
	}
	d := &profileDeps{
		cfg:        cfg,
		name:       name,
		profile:    profile,
		paths:      paths,
		vault:      vault.NewFS(paths.VaultPath),
		embedder:   embeddingsProvider(profile),
		configPath: absCfg,
	}
	if openDB {
		sqlDB, err := db.Open(paths.DBPath)
		if err != nil {
			return nil, err
		}
		if _, err := db.Migrate(sqlDB); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
		d.db = sqlDB
	}
	return d, nil
}

// embeddingsProvider builds the configured embedding provider for a profile.
// Construction is lazy (no network/subprocess), so an unreachable Ollama or a
// missing Apple helper only surfaces when embedding is actually attempted.
func embeddingsProvider(profile config.Profile) embeddings.Provider {
	e := profile.Embeddings
	if e.Provider == "apple" {
		helper := e.Helper
		if helper == "" {
			helper = config.DefaultAppleHelperPath()
		}
		return embeddings.NewApple(helper, e.Model, e.Dim)
	}
	return embeddings.NewOllama(e.Host, e.Model, e.Dim)
}

// claudeAdapter builds the Claude adapter for this profile. In api_key mode it is
// the direct Anthropic API adapter (ADR-008: the only path that bypasses Claude
// Code, in exchange for exact count_tokens + per-token cost). Otherwise it is the
// Claude Code (`claude -p`) adapter on the user's subscription/enterprise login,
// resolving the optional OAuth token for headless automations.
func (d *profileDeps) claudeAdapter() agent.Agent {
	if d.profile.Claude.AuthMode == "api_key" {
		// The key comes from ANTHROPIC_API_KEY (.env-backed) or a config secret ref.
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			key, _ = config.ResolveSecret(d.profile.Claude.OAuthToken)
		}
		return agent.NewAPIKey(key)
	}
	// A failed resolution is NOT fatal here — an interactive `claude login`
	// session in the profile's config dir can still carry the call — but the
	// error rides along so a subsequent auth failure explains itself.
	oauth, oauthErr := config.ResolveSecret(d.profile.Claude.OAuthToken)
	// Agentic runs (ADR-017) spawn this same binary as the MCP server; the
	// adapter appends the per-call read-only --tools filter itself.
	exe, _ := os.Executable()
	return agent.NewClaudeCode(agent.ClaudeCodeOptions{
		ConfigDir:     d.paths.ConfigDir,
		OAuthToken:    oauth,
		OAuthTokenErr: oauthErr,
		AuthMode:      d.profile.Claude.AuthMode,
		MCPCommand:    exe,
		MCPArgs:       []string{"mcp", "--config", d.configPath, "--profile", d.name},
	})
}

// agentRouter composes the per-provider adapters for this profile (ADR-015).
// Claude is always present; local adapters are constructed only when a
// models.* tier references them. Construction is lazy (no network/subprocess),
// matching embeddingsProvider.
func (d *profileDeps) agentRouter() agent.Router {
	r := agent.Router{Claude: d.claudeAdapter()}
	models := d.profile.Models
	for _, tier := range []string{models.Classify, models.Routine, models.Synthesis} {
		switch config.ParseModelRef(tier).Provider {
		case config.ProviderOllama:
			if r.Ollama == nil {
				r.Ollama = agent.NewOllama(models.OllamaHost)
			}
		case config.ProviderApple:
			if r.Apple == nil {
				helper := models.AppleHelper
				if helper == "" {
					helper = config.DefaultAppleLMHelperPath()
				}
				r.Apple = agent.NewAppleFM(helper)
			}
		}
	}
	return r
}

// services bundles the composed runtime services for a profile.
type services struct {
	manager  tokens.Manager
	searcher *search.Searcher
	pipeline *ingestion.Pipeline
	engine   *automations.Engine
}

// buildServices assembles the token-manager chokepoint (with the real claude -p
// adapter), search, a deterministic-enricher pipeline and the automation engine.
// Requires the database to be open.
// buildSearcher constructs the hybrid Searcher with the configured vector
// backend and optional local reranker (ADR-025/ADR-027). Shared by the service
// wiring and the `axon search` CLI so both honour retrieval.rerank.
func (d *profileDeps) buildSearcher() *search.Searcher {
	host := d.profile.Embeddings.Host
	if host == "" {
		host = embeddings.DefaultOllamaHost
	}
	reranker, _ := rerank.RerankerFor(d.profile.Retrieval.Rerank, host) // off/misconfig → nil; doctor surfaces it
	return search.New(d.db, d.embedder).Configure(d.profile.Retrieval).WithReranker(reranker, d.profile.Retrieval.RerankOverfetchOr())
}

func (d *profileDeps) buildServices(bus *events.Bus) services {
	searcher := d.buildSearcher()
	mgr := tokens.NewWithRouter(d.db, d.agentRouter(), searcher, bus, managerConfig(d.name, d.profile, d.cfg))
	ocr, _ := ingestion.OCRFor(d.profile.Ingestion, runtime.GOOS)       // off/misconfig → nil; doctor surfaces it
	vision, _ := ingestion.VisionFor(d.profile.Ingestion, runtime.GOOS) // off/misconfig → nil; doctor surfaces it
	pipeline := &ingestion.Pipeline{
		Vault: d.vault, DB: d.db, Embedder: d.embedder,
		Enricher: ingestion.Heuristic{}, Fetcher: ingestion.NewHTTPFetcher(d.profile.Policy, d.profile.Ingestion.Auth...),
		Policy: d.profile.Policy, Profile: d.name, Bus: bus, OCR: ocr,
		Vision: vision, MediaHosts: d.profile.Ingestion.MediaHosts, CaptionLangs: d.profile.Ingestion.CaptionLangs,
	}
	engine := automations.NewEngine(automations.EngineDeps{
		Profile: d.name, Config: d.profile, DB: d.db, Vault: d.vault,
		Manager: mgr, Searcher: searcher, Embedder: d.embedder, Pipeline: pipeline, Bus: bus,
	})
	return services{manager: mgr, searcher: searcher, pipeline: pipeline, engine: engine}
}

// buildEngine returns just the automation engine (used by run/start).
func (d *profileDeps) buildEngine(bus *events.Bus) *automations.Engine {
	return d.buildServices(bus).engine
}

// evalManager builds a token manager for the eval harness: router-backed (local
// refs reach Ollama) but with local_fallback forced to "fail" so a broken local
// model surfaces as a failed case instead of silently measuring Claude (R5.1
// measurement integrity). The returned resolver maps a model key — a family
// alias or a concrete ref — to the bare model string the manager returns, so the
// runner can flag escalation. Requires the database to be open.
func (d *profileDeps) evalManager(bus *events.Bus) (tokens.Manager, func(string) string) {
	p := d.profile
	p.Models.LocalFallback = "fail"
	mc := managerConfig(d.name, p, d.cfg)
	mc.PromotionGateOff = true // eval measures the real local model (FR-142 guard)
	mgr := tokens.NewWithRouter(d.db, d.agentRouter(), d.buildSearcher(), bus, mc)
	resolve := func(key string) string {
		switch key {
		case "classify":
			key = p.Models.Classify
		case "routine":
			key = p.Models.Routine
		case "synthesis":
			key = p.Models.Synthesis
		}
		return config.ParseModelRef(key).Model
	}
	return mgr, resolve
}

// mcpDeps assembles the dependency set for the MCP server.
func (d *profileDeps) mcpDeps(bus *events.Bus) mcp.Deps {
	svc := d.buildServices(bus)
	return mcp.Deps{
		Profile: d.name, Config: d.profile, DB: d.db, Vault: d.vault,
		Searcher: svc.searcher, Manager: svc.manager, Pipeline: svc.pipeline, Engine: svc.engine,
	}
}

// close releases the database if open.
func (d *profileDeps) close() {
	if d.db != nil {
		_ = d.db.Close()
	}
}
