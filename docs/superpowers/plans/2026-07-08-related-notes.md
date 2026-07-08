# R8 — Ambient Related-Notes Surface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose AXON's existing embeddings as a zero-model "related notes" surface — `axon related <path>`, a `vault_related` MCP tool, and a dashboard panel + `GET /api/related` loopback endpoint.

**Architecture:** A new `Searcher.Related` method resolves a note → its mean chunk vector → nearest *other* notes through the existing ANN `VectorIndex` seam (auto-brute-fallback), deduping chunk hits to notes. Three thin surfaces call it; the dashboard endpoint is gated by a `related_enabled` kill-switch. No new architecture, no ADR — rides ADR-025 (ANN seam) and ADR-023 (dashboard trust boundary).

**Tech Stack:** Go 1.26+, `modernc.org/sqlite` (FTS5 + float32 vectors), cobra CLI, `modelcontextprotocol/go-sdk`, Vite/React SPA in `web/`.

**Spec:** `docs/superpowers/specs/2026-07-08-related-notes-design.md`

## Global Constraints

- **Cardinal rule 1:** No Claude call bypasses the token manager. *R8 makes zero model calls — nothing may reach Claude or Ollama; no embedder call (the note's vector is read from the DB).*
- **Cardinal rule 2:** No vault mutation outside wikilink-safe ops. *R8 is read-only — no `vault.write`/`patch`/`move`, no `fs` writes.*
- **S8:** A fresh clone with all automations off still runs and is useful. *R8 is additive; `related_enabled: false` removes only the dashboard endpoint/tab.*
- **S9:** The vault rebuilds the DB, never the reverse. *R8 persists nothing new — results derive live from `vec_chunks`.*
- **NFR-05:** Fetched/note content is data, never instructions. *The note path is used only to look up an id.*
- Go: `gofmt`/`goimports` clean, `go vet` + `golangci-lint` green. Wrap errors with `%w`. Propagate `context.Context`.
- Run test suites with `env -u FORCE_COLOR` (the ambient shell exports `FORCE_COLOR=3`).
- Tunables are unexported Go consts, **not** config: `relatedDefaultTopK=10`, `relatedMinSimilarity=0.3`, `relatedChunkOverfetch=8`. The only new config key is `dashboard.related_enabled`.
- FR IDs: FR-148 (engine), FR-149 (CLI + MCP), FR-150 (dashboard). No new ADR.
- The ambient GateGuard hook fires a fact-force preamble on the first Write/Edit/Bash each turn and blocks `git commit --amend` / `rm -rf`; comply tersely, use follow-up commits, skip scratch cleanup.

---

### Task 1: Engine — `Searcher.Related` (FR-148)

**Files:**
- Modify: `internal/search/search.go` (add `RelatedNote` type, `Related` method, consts)
- Test: `internal/search/related_test.go` (create)

**Interfaces:**
- Consumes (existing): `db.GetNoteIDByPath(ctx, Queryer, path) (*int64, error)`; `db.NoteMeanVectors(ctx, Queryer2, present map[int64]bool) (map[int64][]float32, error)`; `db.HybridSearch(ctx, DBTX, db.SearchOpts{Query, QueryVector, TopK, Index}) ([]db.ChunkHit, error)`; `db.ChunkHit{NoteID *int64, Path string, Vector float64}`; `(*Searcher).vindex() db.VectorIndex`.
- Produces (for Tasks 2–5): `search.RelatedNote{Path string, Similarity float64}` and `func (s *Searcher) Related(ctx context.Context, notePath string, topK int) ([]RelatedNote, error)`.

- [ ] **Step 1: Write the failing test**

Create `internal/search/related_test.go` (seeds explicit vectors like `search_test.go`, so it is fully deterministic and needs no embedder):

```go
package search

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
)

// seedRelated builds an in-memory DB with three single-chunk notes whose
// vectors have known cosine relationships to the target:
//   target.md → [1,0,0,0]
//   near.md   → [0.9,0.1,0,0]  (cosine ≈ 0.994, above the 0.3 floor)
//   far.md    → [0,0,1,0]      (cosine 0, below the floor → dropped)
func seedRelated(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	seed := func(path string, vec []float32) {
		id, err := db.UpsertNote(ctx, d, db.NoteRow{Path: path, Title: path})
		if err != nil {
			t.Fatal(err)
		}
		cid, err := db.InsertChunk(ctx, d, db.ChunkRow{NoteID: &id, Text: path + " body", ContentHash: path})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertChunkVector(ctx, d, cid, "fake", vec); err != nil {
			t.Fatal(err)
		}
	}
	seed("target.md", []float32{1, 0, 0, 0})
	seed("near.md", []float32{0.9, 0.1, 0, 0})
	seed("far.md", []float32{0, 0, 1, 0})
	return d
}

func TestRelatedRanksExcludesTargetAndFloors(t *testing.T) {
	ctx := context.Background()
	d := seedRelated(t)
	s := New(d, embeddings.NewFake())

	got, err := s.Related(ctx, "target.md", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 related note (near.md), got %d: %+v", len(got), got)
	}
	if got[0].Path != "near.md" {
		t.Errorf("want near.md, got %q", got[0].Path)
	}
	if got[0].Similarity < 0.9 {
		t.Errorf("expected high similarity for near.md, got %.3f", got[0].Similarity)
	}
	for _, r := range got {
		if r.Path == "target.md" {
			t.Error("target note must be excluded from its own related list")
		}
		if r.Path == "far.md" {
			t.Error("far.md is below the floor and must be dropped")
		}
	}
}

func TestRelatedUnknownPathErrors(t *testing.T) {
	ctx := context.Background()
	d := seedRelated(t)
	s := New(d, embeddings.NewFake())
	if _, err := s.Related(ctx, "nope.md", 5); err == nil {
		t.Fatal("expected an error for an unknown note path")
	}
}

func TestRelatedUnembeddedNoteIsEmptyNotError(t *testing.T) {
	ctx := context.Background()
	d := seedRelated(t)
	// A note that exists but has no embedded chunk.
	if _, err := db.UpsertNote(ctx, d, db.NoteRow{Path: "bare.md", Title: "bare"}); err != nil {
		t.Fatal(err)
	}
	s := New(d, embeddings.NewFake())
	got, err := s.Related(ctx, "bare.md", 5)
	if err != nil {
		t.Fatalf("unembedded note should return empty, not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestRelatedAnnBackendMatchesBrute(t *testing.T) {
	ctx := context.Background()
	d := seedRelated(t)
	brute := New(d, embeddings.NewFake())
	// ann configured but below threshold ⇒ IVFIndex auto-falls back to brute
	// (the small-vault guarantee; bit-identical parity at nprobe≥k is proven
	// in the db/IVF tests, ADR-025). This asserts Related threads s.vindex().
	ann := New(d, embeddings.NewFake()).Configure(config.RetrievalConfig{
		Index: "ann", ANN: config.ANNConfig{Threshold: 1000, NProbe: 8},
	})
	gb, err := brute.Related(ctx, "target.md", 5)
	if err != nil {
		t.Fatal(err)
	}
	ga, err := ann.Related(ctx, "target.md", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(gb) != len(ga) {
		t.Fatalf("brute %d vs ann %d results", len(gb), len(ga))
	}
	for i := range gb {
		if gb[i].Path != ga[i].Path {
			t.Errorf("order differs at %d: brute %q ann %q", i, gb[i].Path, ga[i].Path)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/search/ -run TestRelated -v`
Expected: FAIL — `s.Related undefined (type *Searcher has no field or method Related)`.

- [ ] **Step 3: Implement `RelatedNote` + `Related`**

Append to `internal/search/search.go` (after the `Retrieve` method, end of file):

```go
// Related-notes surface (R8/FR-148): the top notes most similar to a given
// note, by pure vector math over the ANN seam — zero model calls, read-only.
const (
	relatedDefaultTopK    = 10  // default result count when topK ≤ 0
	relatedMinSimilarity  = 0.3 // cosine floor; drops clearly-unrelated notes
	relatedChunkOverfetch = 8   // chunk headroom so chunk→note dedup still fills topK
)

// RelatedNote is one entry in a note's related list.
type RelatedNote struct {
	Path       string  `json:"path"`
	Similarity float64 `json:"similarity"` // raw cosine in [-1,1], typically (0,1]
}

// Related returns the notes most similar to notePath, ranked by cosine over the
// note's mean chunk vector. It makes NO model call: the target's vector is read
// from the DB and matched against candidates through the ANN VectorIndex seam
// (ADR-025), which auto-falls back to exact brute below retrieval.ann.threshold.
// An unknown path is an error; a known-but-unembedded note returns an empty list.
func (s *Searcher) Related(ctx context.Context, notePath string, topK int) ([]RelatedNote, error) {
	if topK <= 0 {
		topK = relatedDefaultTopK
	}
	id, err := db.GetNoteIDByPath(ctx, s.DB, notePath)
	if err != nil {
		return nil, err
	}
	if id == nil {
		return nil, fmt.Errorf("note %q not found in the index (run `axon reindex`?)", notePath)
	}
	means, err := db.NoteMeanVectors(ctx, s.DB, map[int64]bool{*id: true})
	if err != nil {
		return nil, err
	}
	mean, ok := means[*id]
	if !ok || len(mean) == 0 {
		return nil, nil // note has no embedded chunks yet
	}
	// Overfetch chunk candidates (empty Query ⇒ vector-only) so chunk→note
	// collapse still yields topK distinct notes after excluding the target.
	hits, err := db.HybridSearch(ctx, s.DB, db.SearchOpts{
		QueryVector: mean,
		TopK:        (topK + 1) * relatedChunkOverfetch,
		Index:       s.vindex(),
	})
	if err != nil {
		return nil, err
	}
	best := map[string]float64{} // note path -> max cosine
	for _, h := range hits {
		if h.NoteID == nil || *h.NoteID == *id || h.Path == "" {
			continue // skip orphan chunks and the target's own chunks
		}
		if h.Vector > best[h.Path] {
			best[h.Path] = h.Vector
		}
	}
	out := make([]RelatedNote, 0, len(best))
	for path, sim := range best {
		if sim >= relatedMinSimilarity {
			out = append(out, RelatedNote{Path: path, Similarity: sim})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Similarity != out[j].Similarity {
			return out[i].Similarity > out[j].Similarity
		}
		return out[i].Path < out[j].Path // stable tie-break
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}
```

Add `"fmt"` and `"sort"` to the import block in `internal/search/search.go` (currently imports `context`, `database/sql`, `strings`, and the internal packages):

```go
import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/rerank"
)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/search/ -v`
Expected: PASS (all `TestRelated*` plus the pre-existing search tests).

- [ ] **Step 5: Commit**

```bash
git add internal/search/search.go internal/search/related_test.go
git commit -m "feat(related): Searcher.Related over the ANN seam (FR-148)"
```

---

### Task 2: CLI — `axon related <path>` (FR-149)

**Files:**
- Create: `cmd/axon/related_cmd.go`
- Modify: `cmd/axon/root.go` (register the command)
- Test: `cmd/axon/related_cmd_test.go` (create)

**Interfaces:**
- Consumes: `search.RelatedNote`, `(*Searcher).Related` (Task 1); `loadProfileDeps(gf, true)`; `(*profileDeps).buildSearcher()`; `(*profileDeps).close()`; `tui.Interactive`, `tui.Table`; `ui.For`, `ui.IconSearch`.
- Produces: `newRelatedCmd(gf *globalFlags) *cobra.Command`.

- [ ] **Step 1: Write the failing test**

Create `cmd/axon/related_cmd_test.go` (dependency-free: an unknown path errors deterministically without needing embeddings/Ollama):

```go
package main

import (
	"testing"
)

// TestRelatedCLIUnknownPathErrors: a path not in the index is a clean error
// (typo detection), needs no model/embedder — exits non-zero.
func TestRelatedCLIUnknownPathErrors(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := run(t, "related", "no-such-note.md", "--json", "--config", cfgPath); err == nil {
		t.Fatal("expected a non-nil error for an unknown note path")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `env -u FORCE_COLOR go test ./cmd/axon/ -run TestRelatedCLI -v`
Expected: FAIL — `unknown command "related" for "axon"` (surfaced through `run`'s error).

- [ ] **Step 3: Create the command**

Create `cmd/axon/related_cmd.go`:

```go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

func newRelatedCmd(gf *globalFlags) *cobra.Command {
	var topK int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "related <note-path>",
		Short: "Notes most similar to a given note — pure vector math, no model call (FR-148)",
		Long: "Surfaces the notes most related to <note-path> using the embeddings AXON\n" +
			"already has. Zero tokens, no Claude/Ollama call. <note-path> is a\n" +
			"vault-relative path, exactly like `axon read`/vault_read.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()

			s := deps.buildSearcher()
			related, err := s.Related(cmd.Context(), args[0], topK)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(related)
			}
			sty := ui.For(out)
			if len(related) == 0 {
				fmt.Fprintf(out, "%s %s\n", sty.Yellow(ui.IconSearch), sty.Dim(fmt.Sprintf("no related notes for %q", args[0])))
				return nil
			}
			if tui.Interactive(out) {
				rows := make([][]string, 0, len(related))
				for i, r := range related {
					rows = append(rows, []string{fmt.Sprintf("%d", i+1), r.Path, fmt.Sprintf("%.3f", r.Similarity)})
				}
				fmt.Fprintf(out, "%s %s\n", ui.IconSearch, sty.Dim(fmt.Sprintf("%d note(s) related to %q", len(related), args[0])))
				tui.Table(out, []string{"#", "PATH", "SIMILARITY"}, rows)
				return nil
			}
			fmt.Fprintf(out, "%s %s\n", ui.IconSearch, sty.Dim(fmt.Sprintf("%d note(s) related to %q", len(related), args[0])))
			for i, r := range related {
				fmt.Fprintf(out, "%s %s  %s\n",
					sty.Dim(fmt.Sprintf("%d.", i+1)),
					sty.Bold(sty.Cyan(r.Path)),
					sty.Dim(fmt.Sprintf("(sim %.3f)", r.Similarity)))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 0, "number of related notes (default 10)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit results as JSON")
	return cmd
}
```

- [ ] **Step 4: Register the command**

In `cmd/axon/root.go`, add `newRelatedCmd(gf)` to the data-command `AddCommand` line (currently `newIngestCmd(gf), newSearchCmd(gf), newAskCmd(gf), newStatusCmd(gf), newSubscribeCmd(gf)`):

```go
root.AddCommand(newIngestCmd(gf), newSearchCmd(gf), newAskCmd(gf), newRelatedCmd(gf), newStatusCmd(gf), newSubscribeCmd(gf))
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `env -u FORCE_COLOR go test ./cmd/axon/ -run TestRelatedCLI -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/axon/related_cmd.go cmd/axon/related_cmd_test.go cmd/axon/root.go
git commit -m "feat(related): axon related CLI command (FR-149)"
```

---

### Task 3: MCP — `vault_related` tool (FR-149)

**Files:**
- Modify: `internal/mcp/tools.go` (add `RelatedIn`/`RelatedOut` + `Related` method)
- Modify: `internal/mcp/server.go` (register in `toolRegistry`)
- Modify: `internal/automations/model.go` (add to `agenticReadTools`)
- Modify: `internal/mcp/filter_test.go` (15 → 16)
- Modify: `internal/mcp/server_test.go` (add to `want` list)
- Test: `internal/mcp/tools_more_test.go` (add `TestRelatedTool`)

**Interfaces:**
- Consumes: `search.RelatedNote`, `(*Searcher).Related` (Task 1); `t.deps.Searcher`.
- Produces: `mcp.RelatedIn{Path string, TopK int}`, `mcp.RelatedOut{Related []search.RelatedNote}`, `(*Tools).Related`.

- [ ] **Step 1: Write the failing tests**

First, update the two count assertions so the suite reflects 16 tools.

In `internal/mcp/filter_test.go`, change line 10:

```go
	if len(all) != 16 {
		t.Fatalf("all tools = %d (%v), want 16", len(all), all)
	}
```

In `internal/mcp/server_test.go`, replace the `want` slice (lines 41-45) with the sorted 16-name list (`vault_related` sorts between `vault_read` and `vault_search`):

```go
	want := []string{
		"automations_list", "automations_run", "daily_append", "knowledge_ingest",
		"knowledge_search", "memory_remember", "metrics_query", "tokens_status",
		"vault_ask", "vault_links", "vault_move", "vault_patch", "vault_read", "vault_related", "vault_search", "vault_write",
	}
```

Then add the behaviour test. Append to `internal/mcp/tools_more_test.go`:

```go
func TestRelatedTool(t *testing.T) {
	ctx := context.Background()
	// Two single-chunk notes with near-identical vectors + one orthogonal.
	tools, _, d := newTestTools(t, map[string]string{
		"target.md": "---\ntitle: Target\n---\nbody\n",
	})
	seed := func(path string, vec []float32) {
		id, err := db.UpsertNote(ctx, d, db.NoteRow{Path: path, Title: path})
		if err != nil {
			t.Fatal(err)
		}
		cid, err := db.InsertChunk(ctx, d, db.ChunkRow{NoteID: &id, Text: path, ContentHash: path})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertChunkVector(ctx, d, cid, "fake", vec); err != nil {
			t.Fatal(err)
		}
	}
	seed("a.md", []float32{1, 0, 0, 0})
	seed("b.md", []float32{0.95, 0.05, 0, 0})
	seed("c.md", []float32{0, 1, 0, 0})

	out, err := tools.Related(ctx, RelatedIn{Path: "a.md", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Related) != 1 || out.Related[0].Path != "b.md" {
		t.Fatalf("want [b.md], got %+v", out.Related)
	}
}
```

> **Adapt the test harness to `newTestTools`'s real return arity.** The existing `server_test.go` calls `newTestTools(t, files)` returning `(tools, v, _)`. Open `internal/mcp/tools_more_test.go` and the test helper (search for `func newTestTools`) to confirm the third return is the `*db.DB`; name the returns `tools, _, d` accordingly. If `newTestTools` does not return the DB handle, seed via `tools.deps.DB` instead (it is the same `*sql.DB`). Ensure `"context"` and the `db` import are present in the test file.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `env -u FORCE_COLOR go test ./internal/mcp/ -run 'TestRelatedTool|TestRegisteredToolNamesFilter|TestServerProtocol' -v`
Expected: FAIL — `tools.Related undefined` and the count assertions now expect 16.

- [ ] **Step 3: Add the tool method**

In `internal/mcp/tools.go`, after the `Ask` method (end of the ask section), add:

```go
// --- related (ambient related-notes, FR-149; zero model call) ---------------

type RelatedIn struct {
	Path string `json:"path" jsonschema:"vault-relative path of the note to find related notes for"`
	TopK int    `json:"top_k,omitempty" jsonschema:"max related notes to return (default 10)"`
}

type RelatedOut struct {
	Related []search.RelatedNote `json:"related"`
}

// Related returns the notes most similar to a note by pure vector math over the
// ANN seam — read-only, spends NO tokens (contrast vault_ask).
func (t *Tools) Related(ctx context.Context, in RelatedIn) (RelatedOut, error) {
	rel, err := t.deps.Searcher.Related(ctx, in.Path, in.TopK)
	if err != nil {
		return RelatedOut{}, err
	}
	return RelatedOut{Related: rel}, nil
}
```

(`search` is already imported in `tools.go`.)

- [ ] **Step 4: Register the tool**

In `internal/mcp/server.go`, add a registry entry after the `vault_ask` entry (before the closing `}` of the returned slice in `toolRegistry`):

```go
		{"vault_related", func(s *mcp.Server, t *Tools) {
			mcp.AddTool(s, &mcp.Tool{Name: "vault_related", Description: "Notes most similar to a given note, by embedding similarity. Read-only and spends NO tokens (unlike vault_ask). Path is vault-relative."},
				func(ctx context.Context, _ *mcp.CallToolRequest, in RelatedIn) (*mcp.CallToolResult, RelatedOut, error) {
					out, err := t.Related(ctx, in)
					return nil, out, err
				})
		}},
```

- [ ] **Step 5: Add to the agentic read allowlist**

In `internal/automations/model.go`, add `vault_related` to `agenticReadTools` (it spends no tokens and only reads — safe for agent runs):

```go
// agenticReadTools is the read surface agentic runs have always had (ADR-017).
var agenticReadTools = map[string]bool{
	"vault_search": true, "vault_read": true, "vault_links": true,
	"knowledge_search": true, "tokens_status": true, "vault_related": true,
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `env -u FORCE_COLOR go test ./internal/mcp/ ./internal/automations/ -v`
Expected: PASS (`TestRelatedTool`, the two count assertions, and the automations allowlist tests).

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/server.go internal/automations/model.go internal/mcp/filter_test.go internal/mcp/server_test.go internal/mcp/tools_more_test.go
git commit -m "feat(related): vault_related MCP tool, agentic-safe (FR-149)"
```

---

### Task 4: Config + dashboard endpoint (FR-150)

**Files:**
- Modify: `internal/config/types.go` (`DashboardConfig.RelatedEnabled` + `RelatedAllowed`)
- Modify: `internal/dashboard/server.go` (`Config.RelatedEnabled`, route, `handleRelated`)
- Modify: `internal/dashboard/health.go` (`related_enabled`)
- Modify: `cmd/axon/start_cmd.go` (wire `RelatedEnabled`)
- Test: `internal/dashboard/related_api_test.go` (create)

**Interfaces:**
- Consumes: `(*Searcher).Related` (Task 1); `config.DashboardConfig`; `writeJSON`, `queryInt`, `guardHost` (existing).
- Produces: `config.DashboardConfig.RelatedAllowed() bool`; `dashboard.Config.RelatedEnabled bool`; `GET /api/related?path=…` handler.

- [ ] **Step 1: Write the failing test**

Create `internal/dashboard/related_api_test.go` (seeds explicit vectors — no Ollama):

```go
package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/search"
)

func relatedTestServer(t *testing.T, enabled bool) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	seed := func(path string, vec []float32) {
		id, err := db.UpsertNote(ctx, d, db.NoteRow{Path: path, Title: path})
		if err != nil {
			t.Fatal(err)
		}
		cid, err := db.InsertChunk(ctx, d, db.ChunkRow{NoteID: &id, Text: path, ContentHash: path})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertChunkVector(ctx, d, cid, "fake", vec); err != nil {
			t.Fatal(err)
		}
	}
	seed("a.md", []float32{1, 0, 0, 0})
	seed("b.md", []float32{0.95, 0.05, 0, 0})

	srv := New(Config{
		DB:             d,
		Searcher:       search.New(d, embeddings.NewFake()),
		RelatedEnabled: enabled,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestRelatedAPIReturnsNeighbours(t *testing.T) {
	ts := relatedTestServer(t, true)
	req, _ := http.NewRequest("GET", ts.URL+"/api/related?path=a.md", nil)
	req.Header.Set("X-Axon-Related", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Related []search.RelatedNote `json:"related"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Related) != 1 || body.Related[0].Path != "b.md" {
		t.Fatalf("want [b.md], got %+v", body.Related)
	}
}

func TestRelatedAPIGuards(t *testing.T) {
	// disabled ⇒ 404
	ts := relatedTestServer(t, false)
	req, _ := http.NewRequest("GET", ts.URL+"/api/related?path=a.md", nil)
	req.Header.Set("X-Axon-Related", "1")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled: status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	ts2 := relatedTestServer(t, true)
	// missing header ⇒ 403
	req2, _ := http.NewRequest("GET", ts2.URL+"/api/related?path=a.md", nil)
	resp2, _ := http.DefaultClient.Do(req2)
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("no header: status = %d, want 403", resp2.StatusCode)
	}
	resp2.Body.Close()
	// missing path ⇒ 400
	req3, _ := http.NewRequest("GET", ts2.URL+"/api/related", nil)
	req3.Header.Set("X-Axon-Related", "1")
	resp3, _ := http.DefaultClient.Do(req3)
	if resp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("no path: status = %d, want 400", resp3.StatusCode)
	}
	resp3.Body.Close()
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/dashboard/ -run TestRelatedAPI -v`
Expected: FAIL — `unknown field RelatedEnabled in struct literal` / `s.handleRelated undefined`.

- [ ] **Step 3: Add the config field + helper**

In `internal/config/types.go`, add to `DashboardConfig` (after `CaptureEnabled`):

```go
	// RelatedEnabled gates the read-only related-notes endpoint (R8/FR-150).
	// Pointer default-ON: unset = enabled; set false to forbid the endpoint.
	RelatedEnabled *bool `yaml:"related_enabled,omitempty"`
```

And after `CaptureAllowed`:

```go
// RelatedAllowed reports whether the dashboard related-notes endpoint is enabled (default true).
func (d DashboardConfig) RelatedAllowed() bool { return d.RelatedEnabled == nil || *d.RelatedEnabled }
```

- [ ] **Step 4: Add the dashboard field, route, and handler**

In `internal/dashboard/server.go`, add to `Config` (after `CaptureEnabled`):

```go
	// RelatedEnabled + Searcher power GET /api/related (R8/FR-150). A nil
	// Searcher or RelatedEnabled=false disables it (404). Zero model calls.
	RelatedEnabled bool
```

Register the route in `Handler()` (after the `GET /capture` line):

```go
	mux.HandleFunc("GET /api/related", s.handleRelated)
```

Add the handler (after `handleAsk`, before `queryInt`):

```go
// handleRelated serves the notes most similar to a given note — read-only,
// zero model calls (contrast handleAsk). Gated by dashboard.related_enabled and
// an X-Axon-Related header (forces a CORS preflight no cross-origin page passes),
// on top of the loopback bind + Host guard. This is also the documented loopback
// endpoint an Obsidian sidebar plugin consumes.
func (s *Server) handleRelated(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.RelatedEnabled || s.cfg.Searcher == nil {
		http.Error(w, "related is disabled for this profile", http.StatusNotFound)
		return
	}
	if r.Header.Get("X-Axon-Related") != "1" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	related, err := s.cfg.Searcher.Related(r.Context(), path, queryInt(r, "top_k", 0))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"related": related})
}
```

- [ ] **Step 5: Expose it in health**

In `internal/dashboard/health.go`, after the `capture_enabled` line:

```go
	out["related_enabled"] = s.cfg.RelatedEnabled
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/dashboard/ ./internal/config/ -v`
Expected: PASS (`TestRelatedAPIReturnsNeighbours`, `TestRelatedAPIGuards`, existing config tests).

- [ ] **Step 7: Wire it in `start_cmd.go`**

In `cmd/axon/start_cmd.go`, add to the `dashboard.Config{…}` literal (after `CaptureEnabled: …`):

```go
					RelatedEnabled: deps.profile.Dashboard.RelatedAllowed(),
```

- [ ] **Step 8: Build to verify wiring compiles**

Run: `env -u FORCE_COLOR go build ./cmd/axon`
Expected: no output (success).

- [ ] **Step 9: Commit**

```bash
git add internal/config/types.go internal/dashboard/server.go internal/dashboard/health.go internal/dashboard/related_api_test.go cmd/axon/start_cmd.go
git commit -m "feat(related): dashboard /api/related endpoint + related_enabled kill-switch (FR-150)"
```

---

### Task 5: Dashboard SPA — Related panel (FR-150)

**Files:**
- Modify: `web/src/App.jsx` (`postRelated`, `RelatedTab`, `TABS`, nav filter, dispatch)

**Interfaces:**
- Consumes: `GET /api/related?path=…` (Task 4); `health.related_enabled`; existing `Card`, `Empty`, `useState`.
- Produces: a `Related` tab.

- [ ] **Step 1: Add the fetch helper + panel component**

In `web/src/App.jsx`, after the `AskTab` component (before the review-tab section), add:

```jsx
/* ── related tab (R8/FR-150) — read-only, zero-model ─────────────────────── */
function getRelated(path) {
  return fetch('/api/related?path=' + encodeURIComponent(path), {
    headers: { 'X-Axon-Related': '1' },
  }).then(async (r) => {
    if (!r.ok) throw new Error(await r.text())
    return r.json()
  })
}

function RelatedTab({ span }) {
  const [p, setP] = useState('')
  const [busy, setBusy] = useState(false)
  const [rows, setRows] = useState(null)
  const [err, setErr] = useState(null)

  const submit = (e) => {
    e.preventDefault()
    if (!p.trim() || busy) return
    setBusy(true); setErr(null); setRows(null)
    getRelated(p.trim())
      .then((d) => setRows(d.related || []))
      .catch((e2) => setErr(String(e2.message || e2)))
      .finally(() => setBusy(false))
  }

  return (
    <Card title="Related notes" meta="embedding similarity — no tokens spent" span={span}>
      <form className="ask-form" onSubmit={submit}>
        <input className="ask-input" placeholder="Vault-relative note path, e.g. 01-Projects/Axon.md"
               value={p} onChange={(e) => setP(e.target.value)} />
        <button type="submit" disabled={busy || !p.trim()}>{busy ? 'Finding…' : 'Find related'}</button>
      </form>
      {err && <Empty>{err}</Empty>}
      {rows && rows.length === 0 && <Empty>No related notes found for that path.</Empty>}
      {rows && rows.length > 0 && (
        <ul className="related-list">
          {rows.map((r) => (
            <li key={r.path}>
              <span className="related-path">{r.path}</span>
              <span className="related-sim">{r.similarity.toFixed(3)}</span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}
```

- [ ] **Step 2: Add the tab to `TABS`, the nav filter, and the dispatch**

Add `['related', 'Related']` to the `TABS` array:

```jsx
const TABS = [
  ['overview', 'Overview'], ['tokens', 'Tokens'], ['automations', 'Automations'], ['review', 'Review'],
  ['ask', 'Ask'], ['related', 'Related'], ['knowledge', 'Knowledge'], ['graph', 'Graph'], ['activity', 'Activity'],
]
```

Extend the nav filter so the Related tab hides when disabled (mirroring the ask filter). Replace the `TABS.filter(...)` line:

```jsx
        {TABS.filter(([id]) => (id !== 'ask' || health?.ask_enabled !== false) && (id !== 'related' || health?.related_enabled !== false)).map(([id, label]) => (
```

Add the tab body dispatch (after the `{tab === 'ask' && <AskTab span="span-12" />}` line):

```jsx
          {tab === 'related' && <RelatedTab span="span-12" />}
```

- [ ] **Step 3: Build the SPA**

Run: `cd web && npm run build`
Expected: Vite writes `web/dist/…`; no errors. (No new SSE event kind — the panel is a pull, so `SSE_KINDS` is unchanged.)

- [ ] **Step 4: Rebuild the binary to embed the assets**

Run: `env -u FORCE_COLOR go build ./cmd/axon`
Expected: success (assets embedded via `embed.FS`).

- [ ] **Step 5: Commit**

```bash
git add web/src/App.jsx web/dist
git commit -m "feat(related): dashboard Related panel (FR-150)"
```

---

### Task 6: Doctor — `relatedCheck`

**Files:**
- Modify: `internal/core/doctor.go` (add `relatedCheck`, register it)
- Test: `internal/core/related_doctor_test.go` (create)

**Interfaces:**
- Consumes: `config.Profile.Dashboard.RelatedAllowed()` (Task 4); `db.CountVectors`; `Check`, `StatusOK` (existing).
- Produces: `relatedCheck(p config.Profile, paths config.ResolvedPaths) Check`.

- [ ] **Step 1: Write the failing test**

Create `internal/core/related_doctor_test.go`:

```go
package core

import (
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestRelatedCheckReportsDisabled(t *testing.T) {
	off := false
	p := config.Profile{Dashboard: config.DashboardConfig{RelatedEnabled: &off}}
	c := relatedCheck(p, p.Paths())
	if c.Status != StatusOK {
		t.Fatalf("status = %v, want OK", c.Status)
	}
	if c.Name != "related" {
		t.Fatalf("name = %q, want related", c.Name)
	}
}
```

> Confirm the `Check` field names (`Name`, `Status`, and the message field) against the struct in `internal/core/doctor.go` — the existing checks construct `Check{name, StatusOK, "…"}` positionally, so field order is `Name, Status, Message`. Match whatever the struct declares.

- [ ] **Step 2: Run the test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestRelatedCheck -v`
Expected: FAIL — `undefined: relatedCheck`.

- [ ] **Step 3: Add the check + register it**

In `internal/core/doctor.go`, add the function (next to `annIndexCheck`):

```go
// relatedCheck reports the related-notes surface (R8): whether the endpoint is
// enabled and how many vectors it has to work with. Advisory — never fails.
// The ANN seam's own health is covered by annIndexCheck; this does not duplicate it.
func relatedCheck(p config.Profile, paths config.ResolvedPaths) Check {
	const name = "related"
	if !p.Dashboard.RelatedAllowed() {
		return Check{name, StatusOK, "related-notes endpoint disabled (dashboard.related_enabled: false)"}
	}
	if _, err := os.Stat(paths.DBPath); err != nil {
		return Check{name, StatusOK, "related-notes enabled; no database yet"}
	}
	d, err := sql.Open("sqlite", paths.DBPath)
	if err != nil {
		return Check{name, StatusOK, "related-notes enabled; database not readable, skipped"}
	}
	defer func() { _ = d.Close() }()
	vectors, err := db.CountVectors(context.Background(), d)
	if err != nil {
		return Check{name, StatusOK, "related-notes enabled; vectors not counted"}
	}
	return Check{name, StatusOK, fmt.Sprintf("related-notes enabled (%d vectors indexed)", vectors)}
}
```

Register it after `annIndexCheck` in the per-profile block (around line 132):

```go
				checks = append(checks, annIndexCheck(p, paths))
				checks = append(checks, relatedCheck(p, paths))
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestRelatedCheck -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/doctor.go internal/core/related_doctor_test.go
git commit -m "feat(related): advisory doctor relatedCheck"
```

---

### Task 7: Docs + full-suite gate + roadmap

**Files:**
- Modify: `docs/03-requirements.md` (FR-148/149/150 rows)
- Modify: `docs/08-*` (MCP tool list — `vault_related`) and/or `docs/09-*` (dashboard endpoint)
- Modify: `docs/15-roadmap-1.2.md` (mark R8 built)
- Modify: `README.md` (command/tool counts if stated)

- [ ] **Step 1: Add the FR rows**

In `docs/03-requirements.md`, add rows tracing the three FRs (match the table format already in the file):
- **FR-148** — `Searcher.Related`: zero-model related-notes engine over the ANN seam (ADR-025).
- **FR-149** — `axon related` CLI + `vault_related` MCP tool (agentic-safe, read-only).
- **FR-150** — dashboard Related panel + `GET /api/related` loopback endpoint, gated by `dashboard.related_enabled`.

- [ ] **Step 2: Update component docs**

Add `vault_related` to the MCP tool list in `docs/08-*` (agent bridge/MCP) and the `/api/related` endpoint + `related_enabled` toggle to `docs/09-*` (dashboard). Grep for `vault_ask` and `capture_enabled` to find the exact tables to extend:

Run: `grep -rn "vault_ask" docs/08-*.md docs/09-*.md; grep -rn "capture_enabled" docs/09-*.md`

- [ ] **Step 3: Mark R8 built in the roadmap**

In `docs/15-roadmap-1.2.md`, update the R8 heading (line ~103) and the build-order table row (line ~138) to reflect BUILT status with the real FR numbers (FR-148/149/150, no ADR), mirroring how R2/R5 were marked.

- [ ] **Step 4: Update README counts**

Run: `grep -n "MCP tool\|15 \|command" README.md` and bump any stated MCP-tool count (15 → 16) or command list to include `axon related`.

- [ ] **Step 5: Full-suite gate**

```bash
env -u FORCE_COLOR go build ./... && \
env -u FORCE_COLOR go test ./... && \
gofmt -l . && \
go vet ./... && \
golangci-lint run
```
Expected: build clean, all tests PASS, `gofmt -l` prints nothing, vet + lint green.

> If `gofmt -l` lists a file, run `gofmt -w <file>` and re-stage. Adding a map entry in `model.go` or a struct field may realign gofmt — expect and absorb that.

- [ ] **Step 6: Commit**

```bash
git add docs/ README.md
git commit -m "docs(R8): FR-148/149/150 rows, roadmap R8 built, tool/endpoint docs"
```

---

## Live smoke (manual, after Task 7 — real Ollama, no Claude needed)

In an isolated scratch env (`AXON_HOME` pointing at a throwaway dir), with Ollama running `nomic-embed-text`:

1. `axon init`, add ~6 topical notes (e.g. three about databases, three about cooking), `axon reindex --embeddings`.
2. `axon related <one-db-note-path>` → the other DB notes rank above the cooking notes; **runs <100 ms warm**; no token-ledger entry appears in `axon status`.
3. `axon related does-not-exist.md` → clean "not found" error, exit non-zero.
4. Start the daemon; `curl -H 'X-Axon-Related: 1' 'http://127.0.0.1:7777/api/related?path=<note>'` → JSON list; the same URL **without** the header → 403.
5. Set `dashboard.related_enabled: false`, restart → the endpoint returns 404 and the Related tab is hidden in the SPA.
6. Confirm `axon doctor` shows the `related` check (enabled, N vectors).

Skip scratch cleanup (the GateGuard hook blocks `rm -rf`).

## Self-review notes (author)

- **Spec coverage:** FR-148 → Task 1; FR-149 → Tasks 2 (CLI) + 3 (MCP); FR-150 → Tasks 4 (endpoint) + 5 (SPA); doctor → Task 6; docs/roadmap → Task 7. Every §-of-spec maps to a task.
- **Type consistency:** `RelatedNote{Path, Similarity}` and `Related(ctx, notePath string, topK int) ([]RelatedNote, error)` are used identically in Tasks 1–5; `RelatedIn/RelatedOut` in Task 3; `RelatedEnabled`/`RelatedAllowed` in Task 4/6.
- **Zero-model invariant:** no task calls an embedder or agent — the target vector is read from `vec_chunks` via `NoteMeanVectors`; `HybridSearch` receives a `QueryVector` and empty `Query`, so no embedding is computed.
- **Two harness adapters flagged inline** (not placeholders): `newTestTools`'s return arity in Task 3, and the `Check` struct field order in Task 6 — both say exactly what to confirm and how.
