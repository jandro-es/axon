package main

import (
	"database/sql"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/ingestion"
	"github.com/jandro-es/axon/internal/mcp"
	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

// profileDeps bundles the per-profile runtime objects a data command needs.
type profileDeps struct {
	cfg      *config.Config
	name     string
	profile  config.Profile
	paths    config.ResolvedPaths
	db       *sql.DB
	vault    *vault.FS
	embedder embeddings.Provider
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
	d := &profileDeps{
		cfg:      cfg,
		name:     name,
		profile:  profile,
		paths:    paths,
		vault:    vault.NewFS(paths.VaultPath),
		embedder: embeddingsProvider(profile),
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
// Construction is lazy (no network), so an unreachable Ollama only surfaces when
// embedding is actually attempted.
func embeddingsProvider(profile config.Profile) embeddings.Provider {
	return embeddings.NewOllama(profile.Embeddings.Host, profile.Embeddings.Model, profile.Embeddings.Dim)
}

// agentAdapter builds the Claude Code (`claude -p`) adapter for this profile,
// resolving the OAuth token from the environment (.env-backed). The token is
// optional — without it, a headless run relies on an interactive `claude login`.
func (d *profileDeps) agentAdapter() agent.Agent {
	oauth, _ := config.ResolveSecret(d.profile.Claude.OAuthToken)
	return agent.NewClaudeCode(agent.ClaudeCodeOptions{
		ConfigDir:  d.paths.ConfigDir,
		OAuthToken: oauth,
		AuthMode:   d.profile.Claude.AuthMode,
	})
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
func (d *profileDeps) buildServices(bus *events.Bus) services {
	searcher := search.New(d.db, d.embedder)
	mgr := tokens.New(d.db, d.agentAdapter(), searcher, bus, managerConfig(d.name, d.profile, d.cfg))
	pipeline := &ingestion.Pipeline{
		Vault: d.vault, DB: d.db, Embedder: d.embedder,
		Enricher: ingestion.Heuristic{}, Fetcher: ingestion.NewHTTPFetcher(),
		Policy: d.profile.Policy, Profile: d.name, Bus: bus,
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
