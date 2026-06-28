package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migration is one forward-only schema step, identified by a leading numeric
// version in its filename (e.g. 0001_init.sql).
type migration struct {
	version int
	name    string
	sql     string
}

// Migrate applies every embedded migration whose version is greater than the
// database's current schema version (tracked via PRAGMA user_version), each in
// its own transaction. It is idempotent: re-running applies nothing and reports
// the unchanged version. Returns the resulting schema version.
func Migrate(sqlDB *sql.DB) (int, error) {
	migs, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	current, err := userVersion(sqlDB)
	if err != nil {
		return 0, err
	}
	for _, m := range migs {
		if m.version <= current {
			continue
		}
		if err := applyMigration(sqlDB, m); err != nil {
			return current, fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
		}
		current = m.version
	}
	return current, nil
}

func applyMigration(sqlDB *sql.DB, m migration) error {
	tx, err := sqlDB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(m.sql); err != nil {
		return err
	}
	// user_version doesn't accept placeholders; the value is a vetted int.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d;", m.version)); err != nil {
		return err
	}
	return tx.Commit()
}

// SchemaVersion returns the database's current migration version.
func SchemaVersion(sqlDB *sql.DB) (int, error) {
	return userVersion(sqlDB)
}

func userVersion(sqlDB *sql.DB) (int, error) {
	var v int
	if err := sqlDB.QueryRow("PRAGMA user_version;").Scan(&v); err != nil {
		return 0, fmt.Errorf("read user_version: %w", err)
	}
	return v, nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var migs []migration
	seen := make(map[int]string)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, err := parseVersion(e.Name())
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("duplicate migration version %d: %s and %s", version, prev, e.Name())
		}
		seen[version] = e.Name()
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		migs = append(migs, migration{
			version: version,
			name:    strings.TrimSuffix(e.Name(), ".sql"),
			sql:     string(body),
		})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })
	return migs, nil
}

// parseVersion extracts the leading integer from a migration filename like
// "0001_init.sql".
func parseVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration %q must be named <version>_<name>.sql", name)
	}
	v, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("migration %q has non-numeric version prefix: %w", name, err)
	}
	return v, nil
}
