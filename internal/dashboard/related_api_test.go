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
