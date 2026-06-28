package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIngestAndSearchCLI(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)

	// init so the vault + DB exist.
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	// A local Markdown article to ingest (no network, no Ollama needed).
	article := filepath.Join(dir, "article.md")
	if err := os.WriteFile(article, []byte("# Knowledge Graphs\n\nKnowledge graphs connect entities with typed edges for retrieval.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "ingest", article, "--config", cfgPath)
	if err != nil {
		t.Fatalf("ingest: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Knowledge Graphs") {
		t.Errorf("ingest output missing title:\n%s", out)
	}

	// Lexical search finds it even without Ollama (vectors pending).
	out, err = run(t, "search", "knowledge graph entities", "--config", cfgPath)
	if err != nil {
		t.Fatalf("search: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Knowledge") {
		t.Errorf("search did not find the ingested note:\n%s", out)
	}

	// Re-ingest unchanged → skip.
	out, err = run(t, "ingest", article, "--config", cfgPath)
	if err != nil {
		t.Fatalf("re-ingest: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skipped") {
		t.Errorf("re-ingest not skipped:\n%s", out)
	}
}
