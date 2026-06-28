package main

import (
	"database/sql"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
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

// close releases the database if open.
func (d *profileDeps) close() {
	if d.db != nil {
		_ = d.db.Close()
	}
}
