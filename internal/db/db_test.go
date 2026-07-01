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
