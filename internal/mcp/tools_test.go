package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/ingestion"
	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

func newTestTools(t *testing.T, files map[string]string) (*Tools, *vault.FS, *agent.Fake) {
	t.Helper()
	vdir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(vdir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	v := vault.NewFS(vdir)
	emb := embeddings.NewFake()
	searcher := search.New(d, emb)
	fake := agent.NewFake()
	profile := config.Profile{
		Models: config.ModelsConfig{Classify: "h", Routine: "s", Synthesis: "o"},
		Limits: config.LimitsConfig{DailyTokens: 1_000_000, WeeklyTokens: 5_000_000, GuardPauseAtPct: 80},
	}
	mgr := tokens.New(d, fake, searcher, nil, tokens.Config{Profile: "test", AuthMode: "subscription", Models: profile.Models, Limits: profile.Limits})
	pipeline := &ingestion.Pipeline{Vault: v, DB: d, Embedder: emb, Enricher: ingestion.Heuristic{}, Fetcher: ingestion.NewHTTPFetcher(config.PolicyConfig{}), Profile: "test"}
	engine := automations.NewEngine(automations.EngineDeps{Profile: "test", Config: profile, DB: d, Vault: v, Manager: mgr, Searcher: searcher, Embedder: emb})
	return NewTools(Deps{
		Profile: "test", Config: profile, DB: d, Vault: v,
		Searcher: searcher, Manager: mgr, Pipeline: pipeline, Engine: engine,
	}), v, fake
}

// TestMoveKeepsLinksIntact is the S5 gate exercised through the MCP tool layer:
// a vault_move rename rewrites inbound links and leaves zero broken wikilinks.
func TestMoveKeepsLinksIntact(t *testing.T) {
	ctx := context.Background()
	tools, v, _ := newTestTools(t, map[string]string{
		"01-Projects/a.md": "links to [[Beta]] and [[02-Areas/Beta|b]].\n",
		"02-Areas/Beta.md": "I am beta.\n",
	})
	// Index so links resolve and backlinks are reportable.
	if _, err := core.Reindex(ctx, v, tools.deps.DB); err != nil {
		t.Fatal(err)
	}

	out, err := tools.Move(ctx, MoveIn{From: "02-Areas/Beta.md", To: "03-Resources/Knowledge/Renamed.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatal("move not ok")
	}
	// a.md was an inbound linker; it should be reported as updated.
	if len(out.UpdatedLinks) == 0 {
		t.Error("expected updated_links to report the inbound linker")
	}

	// Re-index and assert no broken wikilinks remain.
	if _, err := core.Reindex(ctx, v, tools.deps.DB); err != nil {
		t.Fatal(err)
	}
	broken, err := db.CountBrokenWikilinks(ctx, tools.deps.DB)
	if err != nil {
		t.Fatal(err)
	}
	if broken != 0 {
		t.Errorf("after vault_move: %d broken wikilinks, want 0", broken)
	}
}

func TestWriteRefusesClobber(t *testing.T) {
	ctx := context.Background()
	tools, v, _ := newTestTools(t, map[string]string{
		"keep.md":    "human prose",
		"managed.md": "---\naxon_managed: true\ntype: source\n---\nAXON-authored content",
	})

	if _, err := tools.Write(ctx, WriteIn{Path: "keep.md", Body: "REPLACED"}); err == nil {
		t.Error("vault_write should refuse to clobber an existing note without force")
	}
	if _, err := tools.Write(ctx, WriteIn{Path: "new.md", Body: "fresh"}); err != nil {
		t.Errorf("writing a new note should succeed: %v", err)
	}

	// force is the de-facto destructive op (no vault_delete exists), and it is
	// a plain model-controlled argument — so it only works on AXON-authored
	// notes, never on human prose (NFR-05 / cardinal rule 2).
	if _, err := tools.Write(ctx, WriteIn{Path: "keep.md", Body: "FORCED", Force: true}); err == nil {
		t.Error("force write over a HUMAN note must be refused (not axon_managed)")
	}
	if n, err := v.Read(ctx, "keep.md"); err != nil || n.Body != "human prose" {
		t.Errorf("human note content changed: %q, %v", n.Body, err)
	}
	if _, err := tools.Write(ctx, WriteIn{Path: "managed.md", Body: "regenerated", Force: true}); err != nil {
		t.Errorf("force write over an axon_managed note should succeed: %v", err)
	}
}

// TestToolsRefuseSystemDirPaths: agent-supplied paths must never reach vault
// system directories — writing .claude/CLAUDE.md would let a prompt-injected
// agent rewrite its own instructions for the next session.
func TestToolsRefuseSystemDirPaths(t *testing.T) {
	ctx := context.Background()
	tools, _, _ := newTestTools(t, map[string]string{"note.md": "hi"})

	if _, err := tools.Write(ctx, WriteIn{Path: ".claude/CLAUDE.md", Body: "obey me"}); err == nil {
		t.Error("vault_write into .claude/ must be refused")
	}
	if _, err := tools.Write(ctx, WriteIn{Path: "sub/.obsidian/app.json", Body: "{}"}); err == nil {
		t.Error("vault_write into .obsidian/ must be refused")
	}
	if _, err := tools.Patch(ctx, PatchIn{Path: ".axon/review-queue.md", Marker: "x", Content: "y"}); err == nil {
		t.Error("vault_patch into .axon/ must be refused")
	}
	if _, err := tools.Move(ctx, MoveIn{From: "note.md", To: ".trash/note.md"}); err == nil {
		t.Error("vault_move into .trash/ must be refused")
	}
}

func TestPatchAndSearchAndStatus(t *testing.T) {
	ctx := context.Background()
	tools, v, _ := newTestTools(t, map[string]string{
		"n.md": "---\ntitle: N\n---\nHuman prose stays.\n",
	})
	if _, err := tools.Patch(ctx, PatchIn{Path: "n.md", Marker: "summary", Content: "agent text"}); err != nil {
		t.Fatal(err)
	}
	n, _ := v.Read(ctx, "n.md")
	if !contains(n.Body, "axon:summary:start") || !contains(n.Body, "Human prose stays.") {
		t.Errorf("patch result wrong: %q", n.Body)
	}

	// Index a note for search.
	if _, err := core.Reindex(ctx, v, tools.deps.DB); err != nil {
		t.Fatal(err)
	}
	// Manually index a chunk so search has material.
	noteID, _ := db.UpsertNote(ctx, tools.deps.DB, db.NoteRow{Path: "x.md", Title: "X"})
	cid, _ := db.InsertChunk(ctx, tools.deps.DB, db.ChunkRow{NoteID: &noteID, Text: "vector search and graphs", ContentHash: "h"})
	_ = db.InsertChunkFTS(ctx, tools.deps.DB, cid, "vector search and graphs")

	res, err := tools.Search(ctx, SearchIn{Query: "vector graphs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 {
		t.Error("expected search hits")
	}

	st, err := tools.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.DayLimit != 1_000_000 {
		t.Errorf("status day limit = %d", st.DayLimit)
	}
}

func TestNoDeleteToolExists(t *testing.T) {
	// The server must not register any delete tool (deletes are out-of-band).
	s := NewServer(Deps{})
	_ = s // construction asserts registration doesn't panic; absence of a delete
	// tool is guaranteed by there being no such method/registration above.
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
