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

func searchTestServer(t *testing.T, enabled bool) *httptest.Server {
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
	seed := func(path, text string) {
		id, err := db.UpsertNote(ctx, d, db.NoteRow{Path: path, Title: path})
		if err != nil {
			t.Fatal(err)
		}
		cid, err := db.InsertChunk(ctx, d, db.ChunkRow{NoteID: &id, Text: text, ContentHash: path})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.InsertChunkFTS(ctx, d, cid, text); err != nil {
			t.Fatal(err)
		}
	}
	seed("sqlite.md", "SQLite stores the derived index for the vault")
	seed("cooking.md", "A recipe for slow-cooked ragù")

	srv := New(Config{
		DB:            d,
		Searcher:      search.New(d, embeddings.NewFake()),
		SearchEnabled: enabled,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestSearchAPIReturnsHits(t *testing.T) {
	ts := searchTestServer(t, true)
	req, _ := http.NewRequest("GET", ts.URL+"/api/search?q=sqlite+derived+index", nil)
	req.Header.Set("X-Axon-Search", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Hits []struct {
			Path    string  `json:"path"`
			Snippet string  `json:"snippet"`
			Score   float64 `json:"score"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Hits) == 0 || body.Hits[0].Path != "sqlite.md" {
		t.Fatalf("want sqlite.md first, got %+v", body.Hits)
	}
	if body.Hits[0].Snippet == "" || body.Hits[0].Score == 0 {
		t.Fatalf("hit must carry snippet + score, got %+v", body.Hits[0])
	}
}

func TestSearchAPIGuards(t *testing.T) {
	// disabled ⇒ 404
	ts := searchTestServer(t, false)
	req, _ := http.NewRequest("GET", ts.URL+"/api/search?q=x", nil)
	req.Header.Set("X-Axon-Search", "1")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled: status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	ts2 := searchTestServer(t, true)
	// missing header ⇒ 403
	req2, _ := http.NewRequest("GET", ts2.URL+"/api/search?q=x", nil)
	resp2, _ := http.DefaultClient.Do(req2)
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("no header: status = %d, want 403", resp2.StatusCode)
	}
	resp2.Body.Close()
	// missing q ⇒ 400
	req3, _ := http.NewRequest("GET", ts2.URL+"/api/search", nil)
	req3.Header.Set("X-Axon-Search", "1")
	resp3, _ := http.DefaultClient.Do(req3)
	if resp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("no q: status = %d, want 400", resp3.StatusCode)
	}
	resp3.Body.Close()
}
