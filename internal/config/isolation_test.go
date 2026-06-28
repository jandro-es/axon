package config

import (
	"slices"
	"testing"
)

// TestProfileIsolation is the S7 / NFR-04 gate at the config layer: the personal
// and work profiles must share NO data dir, vault, Claude config dir, db file or
// OAuth-token reference, and the work profile must be the more constrained one.
func TestProfileIsolation(t *testing.T) {
	cfg, err := Load(exampleConfigPath(t))
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Profiles["personal"].Paths()
	w := cfg.Profiles["work"].Paths()

	disjoint := map[string][2]string{
		"vault_path": {p.VaultPath, w.VaultPath},
		"data_dir":   {p.DataDir, w.DataDir},
		"db_path":    {p.DBPath, w.DBPath},
		"config_dir": {p.ConfigDir, w.ConfigDir},
	}
	for field, pair := range disjoint {
		if pair[0] == pair[1] {
			t.Errorf("%s is shared across profiles (%q); profiles must be isolated", field, pair[0])
		}
	}

	// Separate Claude accounts: distinct OAuth-token references.
	pt := cfg.Profiles["personal"].Claude.OAuthToken
	wt := cfg.Profiles["work"].Claude.OAuthToken
	if pt == wt {
		t.Errorf("profiles share an OAuth-token reference %q; accounts must be separate", pt)
	}

	// Distinct auth modes (Max vs Enterprise).
	if cfg.Profiles["personal"].Claude.AuthMode == cfg.Profiles["work"].Claude.AuthMode {
		t.Error("expected distinct auth_mode per profile (subscription vs enterprise)")
	}

	// Work is demonstrably more constrained.
	pw := cfg.Profiles["personal"].Policy
	wp := cfg.Profiles["work"].Policy
	if len(wp.AllowedAutomations) == 0 || slices.Contains(wp.AllowedAutomations, "*") {
		t.Error("work profile should have a restrictive allowed_automations list")
	}
	if !slices.Contains(pw.AllowedAutomations, "*") {
		t.Error("personal profile is expected to permit all automations")
	}
	if len(wp.RedactionRules) == 0 {
		t.Error("work profile should enable redaction rules")
	}
	if !slices.Contains(wp.IngestDomainsDeny, "*") {
		t.Error("work profile should deny ingest domains by default")
	}
}
