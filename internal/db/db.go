// Package db owns the single SQLite file per profile: relational tables, FTS5
// lexical search and brute-force vector search over float32 BLOBs (ADR-002 as
// amended by ADR-010). The database is derived and disposable — the vault is the
// source of truth (ADR-006), so `axon reindex` can always rebuild it.
package db

import (
	"database/sql"
	"fmt"
	"strings"

	// Pure-Go SQLite (cgo-free transpilation of current SQLite, FTS5 built in)
	// so the daemon stays a single static binary. Registers the "sqlite" driver.
	_ "modernc.org/sqlite"
)

// MemoryDSN opens a private in-memory database, used by tests.
const MemoryDSN = ":memory:"

// Open opens (creating if needed) the SQLite database at path and applies the
// pragmas AXON relies on: foreign keys on, WAL journaling (file DBs), busy
// timeout. Pass MemoryDSN for an ephemeral test database.
//
// The pragmas are baked into the DSN, NOT Exec'd once after opening: database/sql
// silently replaces connections after driver.ErrBadConn, and a replacement
// connection would otherwise come up with SQLite defaults (foreign_keys=OFF),
// quietly disabling every cascade the schema depends on.
func Open(path string) (*sql.DB, error) {
	pragmas := []string{
		"_pragma=foreign_keys(1)",
		"_pragma=busy_timeout(5000)",
	}
	var dsn string
	if path == MemoryDSN {
		dsn = "file::memory:?" + strings.Join(pragmas, "&")
	} else {
		pragmas = append(pragmas, "_pragma=journal_mode(WAL)")
		dsn = "file:" + path + "?" + strings.Join(pragmas, "&")
	}
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	// SQLite is single-writer; cap the pool to avoid lock contention surprises.
	sqlDB.SetMaxOpenConns(1)
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", path, err)
	}
	// Sanity-check that the DSN pragma syntax was honored — a silent fallback to
	// foreign_keys=OFF would be integrity-destroying, so fail loudly instead.
	var fk int
	if err := sqlDB.QueryRow("PRAGMA foreign_keys;").Scan(&fk); err != nil || fk != 1 {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("sqlite %q: foreign_keys pragma not applied (got %d, err %v)", path, fk, err)
	}
	return sqlDB, nil
}
