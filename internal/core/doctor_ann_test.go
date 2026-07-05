package core

import (
	"path/filepath"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

func TestAnnIndexCheckWarnsWhenBruteAboveThreshold(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	seedCoreVectors(t, d, 30)
	_ = d.Close()

	p := config.Profile{Retrieval: config.RetrievalConfig{Index: "brute", ANN: config.ANNConfig{Threshold: 10}}}
	paths := config.ResolvedPaths{DBPath: dbPath}
	c := annIndexCheck(p, paths)
	if c.Status != StatusWarn {
		t.Fatalf("status = %q, want warn (30 vectors > threshold 10 in brute mode)", c.Status)
	}
}

func TestAnnIndexCheckWarnsWhenAnnUnbuilt(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	seedCoreVectors(t, d, 30)
	_ = d.Close()

	p := config.Profile{Retrieval: config.RetrievalConfig{Index: "ann", ANN: config.ANNConfig{Threshold: 10}}}
	paths := config.ResolvedPaths{DBPath: dbPath}
	c := annIndexCheck(p, paths)
	if c.Status != StatusWarn {
		t.Fatalf("status = %q, want warn (ann enabled, no centroids)", c.Status)
	}
}
