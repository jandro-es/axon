// Package db owns the single SQLite file per profile (relational + later
// sqlite-vec + FTS5, per ADR-002). Phase 0 provides the connection and a
// forward-only migration runner; the vector/lexical virtual tables arrive in
// Phase 2. The database is derived and disposable — the vault is the source of
// truth (ADR-006), so `axon reindex` can always rebuild it.
package db

import (
	"database/sql"
	"fmt"

	// Pure-Go SQLite driver (wazero/WASM, no cgo) so the daemon stays a single
	// static binary. Registers the "sqlite3" driver and embeds the engine.
	_ "github.com/ncruces/go-sqlite3/driver"
)

// MemoryDSN opens a private in-memory database, used by tests.
const MemoryDSN = ":memory:"

// Open opens (creating if needed) the SQLite database at path and applies the
// pragmas AXON relies on: foreign keys on, WAL journaling, busy timeout. Pass
// MemoryDSN for an ephemeral test database.
func Open(path string) (*sql.DB, error) {
	dsn := path
	if path != MemoryDSN {
		// file: DSN lets ncruces apply pragmas via query params and creates the
		// file if absent.
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	}
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	// SQLite is single-writer; cap the pool to avoid lock contention surprises.
	sqlDB.SetMaxOpenConns(1)
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", path, err)
	}
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	return sqlDB, nil
}
