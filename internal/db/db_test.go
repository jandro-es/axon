package db

import (
	"testing"
)

func TestOpenAndMigrateInMemory(t *testing.T) {
	sqlDB, err := Open(MemoryDSN)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	v, err := Migrate(sqlDB)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if v < 1 {
		t.Fatalf("schema version = %d, want >= 1", v)
	}

	// Every Phase 0 table must exist.
	wantTables := []string{
		"notes", "links", "sources", "chunks",
		"token_ledger", "runs", "budget_state", "events",
	}
	for _, tbl := range wantTables {
		var name string
		err := sqlDB.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&name)
		if err != nil {
			t.Errorf("expected table %q to exist: %v", tbl, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	sqlDB, err := Open(MemoryDSN)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	v1, err := Migrate(sqlDB)
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	v2, err := Migrate(sqlDB)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if v1 != v2 {
		t.Errorf("re-running Migrate changed version: %d -> %d", v1, v2)
	}
}

// TestMigrateUpgradePathPreservesData: a database created at schema version 1
// must upgrade through the remaining migrations without losing rows — the
// scenario every existing installation hits on upgrade.
func TestMigrateUpgradePathPreservesData(t *testing.T) {
	sqlDB, err := Open(MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	migs, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) < 2 {
		t.Skip("only one migration exists; no upgrade path to test")
	}

	// Apply ONLY the first migration, then seed data an old installation
	// would have.
	if err := applyMigration(sqlDB, migs[0]); err != nil {
		t.Fatal(err)
	}
	if v, _ := userVersion(sqlDB); v != migs[0].version {
		t.Fatalf("seed version = %d, want %d", v, migs[0].version)
	}
	if _, err := sqlDB.Exec(`INSERT INTO notes (path, title, content_hash) VALUES ('old.md', 'Old', 'h1');`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO token_ledger (ts, profile, operation, model, input_tokens, output_tokens)
		 VALUES ('2026-01-01T00:00:00Z', 'p', 'op', 'm', 10, 5);`); err != nil {
		t.Fatal(err)
	}

	// Upgrade to latest.
	v, err := Migrate(sqlDB)
	if err != nil {
		t.Fatalf("upgrade from v%d failed: %v", migs[0].version, err)
	}
	if v != migs[len(migs)-1].version {
		t.Errorf("upgraded version = %d, want %d", v, migs[len(migs)-1].version)
	}

	// Seeded data survives the upgrade.
	var notes, ledger int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM notes;`).Scan(&notes); err != nil || notes != 1 {
		t.Errorf("notes after upgrade = %d (err %v), want 1", notes, err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM token_ledger;`).Scan(&ledger); err != nil || ledger != 1 {
		t.Errorf("ledger rows after upgrade = %d (err %v), want 1", ledger, err)
	}

	// And the post-v1 schema objects exist (FTS index from 0002).
	var name string
	if err := sqlDB.QueryRow(
		`SELECT name FROM sqlite_master WHERE name = 'fts_chunks';`).Scan(&name); err != nil {
		t.Errorf("fts_chunks missing after upgrade: %v", err)
	}
}

func TestOpenCreatesFileDatabase(t *testing.T) {
	path := t.TempDir() + "/db.sqlite"
	sqlDB, err := Open(path)
	if err != nil {
		t.Fatalf("Open file db: %v", err)
	}
	defer sqlDB.Close()
	if _, err := Migrate(sqlDB); err != nil {
		t.Fatalf("Migrate file db: %v", err)
	}

	// Foreign keys must be enforced.
	var fk int
	if err := sqlDB.QueryRow("PRAGMA foreign_keys;").Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}

	// WAL journaling on file databases (comes via the DSN, so any replacement
	// connection after driver.ErrBadConn keeps it too).
	var mode string
	if err := sqlDB.QueryRow("PRAGMA journal_mode;").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode pragma: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}

	// And cascades actually fire (the pragma is real, not just reported).
	if _, err := sqlDB.Exec(`INSERT INTO notes (path, title) VALUES ('x.md', 'X');`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chunks (note_id, ordinal, text) SELECT id, 0, 'body' FROM notes;`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`DELETE FROM notes;`); err != nil {
		t.Fatal(err)
	}
	var orphans int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM chunks;`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("chunks after note delete = %d, want 0 (FK cascade must fire)", orphans)
	}
}
