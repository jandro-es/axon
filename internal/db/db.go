// Package db owns the single SQLite file per profile: relational tables, FTS5
// lexical search and brute-force vector search over float32 BLOBs (ADR-002 as
// amended by ADR-010). The database is derived and disposable — the vault is the
// source of truth (ADR-006), so `axon reindex` can always rebuild it.
package db

import (
	"database/sql"
	"fmt"

	// Pure-Go SQLite (cgo-free transpilation of current SQLite, FTS5 built in)
	// so the daemon stays a single static binary. Registers the "sqlite" driver.
	_ "modernc.org/sqlite"
)

// MemoryDSN opens a private in-memory database, used by tests.
const MemoryDSN = ":memory:"

// Open opens (creating if needed) the SQLite database at path and applies the
// pragmas AXON relies on: foreign keys on, WAL journaling (file DBs), busy
// timeout. Pass MemoryDSN for an ephemeral test database.
func Open(path string) (*sql.DB, error) {
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	// SQLite is single-writer; cap the pool to avoid lock contention surprises.
	sqlDB.SetMaxOpenConns(1)
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", path, err)
	}
	pragmas := []string{
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
	}
	if path != MemoryDSN {
		pragmas = append(pragmas, "PRAGMA journal_mode = WAL;")
	}
	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("apply %q: %w", p, err)
		}
	}
	return sqlDB, nil
}
