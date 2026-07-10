package ingestion

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/vault"
)

// countingFetcher records whether Fetch was called, to prove a denied domain
// never reaches the network.
type countingFetcher struct{ calls int }

func (c *countingFetcher) Fetch(ctx context.Context, url string) (*Document, error) {
	c.calls++
	return &Document{URL: url, Body: []byte("should not be fetched")}, nil
}

func newTestPipeline(t *testing.T, policy config.PolicyConfig) (*Pipeline, *embeddings.Fake, *countingFetcher) {
	t.Helper()
	vdir := t.TempDir()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	emb := embeddings.NewFake()
	fetch := &countingFetcher{}
	return &Pipeline{
		Vault:    vault.NewFS(vdir),
		DB:       d,
		Embedder: emb,
		Enricher: Heuristic{},
		Fetcher:  fetch,
		Policy:   policy,
		Profile:  "test",
	}, emb, fetch
}

// writeFile drops a local Markdown file under the pipeline's temp area and
// returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func openPolicy() config.PolicyConfig {
	return config.PolicyConfig{
		EgressAllowlist:    []string{"*"},
		IngestDomainsAllow: []string{"*"},
	}
}

func TestIngestLocalFileProducesRetrievableNote(t *testing.T) {
	p, emb, _ := newTestPipeline(t, openPolicy())
	dir := t.TempDir()
	ctx := context.Background()

	file := writeFile(t, dir, "rrf.md",
		"# Reciprocal Rank Fusion\n\nRRF blends lexical BM25 search with semantic vector search. "+
			"It scores documents by rank position across lists.\n")

	res, err := p.Ingest(ctx, file, IngestOptions{AllowLocalFiles: true})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if res.Status != "ok" {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	if res.Title != "Reciprocal Rank Fusion" {
		t.Errorf("title = %q", res.Title)
	}
	if res.Chunks == 0 || res.Embedded == 0 {
		t.Errorf("expected chunks embedded, got chunks=%d embedded=%d", res.Chunks, res.Embedded)
	}
	if !strings.HasPrefix(res.NotePath, KnowledgeDir+"/") {
		t.Errorf("note path %q not under %s", res.NotePath, KnowledgeDir)
	}

	// The note exists with a summary managed block.
	n, err := p.Vault.Read(ctx, res.NotePath)
	if err != nil {
		t.Fatal(err)
	}
	if n.FrontmatterString("type") != "source" {
		t.Errorf("note type = %q, want source", n.FrontmatterString("type"))
	}
	if !strings.Contains(n.Body, "axon:summary:start") {
		t.Error("note missing summary managed block")
	}

	// It is retrievable via hybrid search.
	s := search.New(p.DB, emb)
	hits, err := s.Search(ctx, "vector search fusion", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Path != res.NotePath {
		t.Errorf("search did not return the ingested note; hits=%+v", hits)
	}
}

func TestIngestImageWritesNoteWithEmbed(t *testing.T) {
	p, _, _ := newTestPipeline(t, openPolicy())
	p.Vision = &fakeVision{text: strings.Repeat("a screenshot of a dashboard ", 8)}
	p.OCR = &fakeOCR{text: ""} // sparse → vision used
	ctx := context.Background()

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(imgPath, []byte("PNGDATA-unique-1"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := p.Ingest(ctx, imgPath, IngestOptions{AllowLocalFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "ok" && res.Status != "redacted" {
		t.Fatalf("status = %q", res.Status)
	}
	note, err := p.Vault.Read(ctx, res.NotePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note.Body, "![[attachments/") {
		t.Fatalf("note missing image embed:\n%s", note.Body)
	}
	if !p.Vault.Exists(AttachmentsDir + "/" + config.ContentHash("PNGDATA-unique-1") + ".png") {
		t.Fatal("attachment file not archived")
	}

	// Re-ingest same bytes → skipped (idempotent by image-byte hash).
	res2, err := p.Ingest(ctx, imgPath, IngestOptions{AllowLocalFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Status != "skipped" {
		t.Fatalf("re-ingest status = %q, want skipped", res2.Status)
	}
}

func TestIngestImageAgentPathRefused(t *testing.T) {
	p, _, _ := newTestPipeline(t, openPolicy())
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(imgPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Ingest(context.Background(), imgPath, IngestOptions{AllowLocalFiles: false}); err == nil {
		t.Fatal("agent-driven image ingestion must be refused")
	}
}

func TestIngestSecondSourceGetsGroundedSuggestion(t *testing.T) {
	p, _, _ := newTestPipeline(t, openPolicy())
	dir := t.TempDir()
	ctx := context.Background()

	// First source establishes content to link to.
	a := writeFile(t, dir, "a.md", "# Vector Databases\n\nVector databases index embeddings for semantic search and retrieval.\n")
	if _, err := p.Ingest(ctx, a, IngestOptions{AllowLocalFiles: true}); err != nil {
		t.Fatal(err)
	}
	// Second, related source should suggest linking to the first (S6 "≥1 link").
	b := writeFile(t, dir, "b.md", "# Semantic Search\n\nSemantic search over embeddings powers modern retrieval and vector databases.\n")
	res, err := p.Ingest(ctx, b, IngestOptions{AllowLocalFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Suggestions) == 0 {
		t.Fatal("expected ≥1 grounded link suggestion for the related source")
	}
	// The suggestion queue should have been written.
	if !p.Vault.Exists(reviewQueuePath) {
		t.Error("review queue not written for suggestions")
	}
}

func TestReingestUnchangedSkipsWithNoEmbedding(t *testing.T) {
	p, emb, _ := newTestPipeline(t, openPolicy())
	dir := t.TempDir()
	ctx := context.Background()
	file := writeFile(t, dir, "x.md", "# Title\n\nStable content that will not change between runs.\n")

	if _, err := p.Ingest(ctx, file, IngestOptions{AllowLocalFiles: true}); err != nil {
		t.Fatal(err)
	}
	callsAfterFirst := emb.EmbedCalls()

	res, err := p.Ingest(ctx, file, IngestOptions{AllowLocalFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "skipped" {
		t.Errorf("re-ingest status = %q, want skipped", res.Status)
	}
	if emb.EmbedCalls() != callsAfterFirst {
		t.Errorf("skip path still embedded: calls %d -> %d", callsAfterFirst, emb.EmbedCalls())
	}
}

func TestDeniedDomainFailsBeforeFetch(t *testing.T) {
	// Work-style policy: deny-by-default, only an internal host allowed.
	policy := config.PolicyConfig{
		EgressAllowlist:    []string{"localhost"},
		IngestDomainsAllow: []string{"docs.internal.example.com"},
		IngestDomainsDeny:  []string{"*"},
	}
	p, _, fetch := newTestPipeline(t, policy)

	_, err := p.Ingest(context.Background(), "https://evil.example.com/article", IngestOptions{AllowLocalFiles: true})
	if err == nil {
		t.Fatal("expected a policy error for a denied domain")
	}
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Errorf("error = %v, want *PolicyError", err)
	}
	if fetch.calls != 0 {
		t.Errorf("fetcher was called %d times; a denied domain must fail before fetch", fetch.calls)
	}
}

func TestRedactionAppliedBeforePersist(t *testing.T) {
	policy := openPolicy()
	policy.RedactionRules = []string{`AKIA[0-9A-Z]{16}`}
	p, _, _ := newTestPipeline(t, policy)
	dir := t.TempDir()
	ctx := context.Background()

	file := writeFile(t, dir, "leak.md", "# Config\n\nThe key is AKIA1234567890ABCDEF and must be scrubbed.\n")
	res, err := p.Ingest(ctx, file, IngestOptions{AllowLocalFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Redacted {
		t.Error("expected Redacted=true")
	}
	n, _ := p.Vault.Read(ctx, res.NotePath)
	if strings.Contains(n.Body, "AKIA1234567890ABCDEF") {
		t.Error("secret was persisted to the note; redaction failed")
	}
	if !strings.Contains(n.Body, "[REDACTED]") {
		t.Error("expected redaction placeholder in note body")
	}
}

func TestLocalFileRefusedOnAgentPath(t *testing.T) {
	p, _, _ := newTestPipeline(t, openPolicy())
	dir := t.TempDir()
	secret := writeFile(t, dir, "secret.md", "# Secret\n\nlocal file contents\n")

	// Default (AllowLocalFiles=false) is the agent/MCP path — must refuse.
	if _, err := p.Ingest(context.Background(), secret, IngestOptions{}); err == nil {
		t.Error("agent-path local-file ingestion must be refused")
	}
	if _, err := p.Ingest(context.Background(), "file://"+secret, IngestOptions{}); err == nil {
		t.Error("agent-path file:// ingestion must be refused")
	}
	// The note must not have been written.
	if n, _ := db.CountChunks(context.Background(), p.DB); n != 0 {
		t.Errorf("refused ingestion still wrote %d chunks", n)
	}
}

func TestRedactionScrubsTitle(t *testing.T) {
	policy := openPolicy()
	policy.RedactionRules = []string{`AKIA[0-9A-Z]{16}`}
	p, _, _ := newTestPipeline(t, policy)
	dir := t.TempDir()
	ctx := context.Background()

	// Secret in the H1 (becomes the title) — must not survive into title/frontmatter.
	file := writeFile(t, dir, "leak.md", "# Key AKIA1234567890ABCDEF here\n\nBody.\n")
	res, err := p.Ingest(ctx, file, IngestOptions{AllowLocalFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Title, "AKIA1234567890ABCDEF") {
		t.Errorf("secret leaked into title: %q", res.Title)
	}
	n, _ := p.Vault.Read(ctx, res.NotePath)
	if strings.Contains(n.FrontmatterString("title"), "AKIA1234567890ABCDEF") {
		t.Errorf("secret leaked into frontmatter title")
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	p, emb, _ := newTestPipeline(t, openPolicy())
	dir := t.TempDir()
	ctx := context.Background()
	file := writeFile(t, dir, "d.md", "# Draft\n\nThis is a dry run and should not be written or embedded.\n")

	res, err := p.Ingest(ctx, file, IngestOptions{DryRun: true, AllowLocalFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "dry-run" {
		t.Errorf("status = %q, want dry-run", res.Status)
	}
	// Dry-run computes the intended note + grounded suggestions (which embeds the
	// query — local and free) but must persist nothing.
	_ = emb
	if p.Vault.Exists(res.NotePath) {
		t.Error("dry-run wrote a note")
	}
	if n, _ := db.CountChunks(ctx, p.DB); n != 0 {
		t.Errorf("dry-run persisted %d chunks", n)
	}
	if n, _ := db.CountVectors(ctx, p.DB); n != 0 {
		t.Errorf("dry-run stored %d vectors", n)
	}
}
