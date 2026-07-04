package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/vault"
)

func reviewTestServer(t *testing.T) (*httptest.Server, *vault.FS, *events.Bus) {
	t.Helper()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	v := vault.NewFS(t.TempDir())
	if err := os.MkdirAll(filepath.Join(v.Root(), ".axon"), 0o755); err != nil {
		t.Fatal(err)
	}
	queue := "## Link suggestions for [[notes/a]] (2026-07-04 10:00)\n- [ ] link to [[notes/b]]\n"
	if err := os.WriteFile(filepath.Join(v.Root(), ".axon", "review-queue.md"), []byte(queue), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Create("notes/a.md", "---\ntitle: a\n---\nbody\n"); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	t.Cleanup(bus.Close)
	s := New(Config{Profile: "test", DB: d, Vault: v, Bus: bus})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, v, bus
}

func TestReviewGetAndAccept(t *testing.T) {
	srv, v, _ := reviewTestServer(t)

	res, err := http.Get(srv.URL + "/api/review")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Items   []map[string]any `json:"items"`
		Pending int              `json:"pending"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Pending != 1 || len(out.Items) != 1 {
		t.Fatalf("review = %+v", out)
	}
	id := out.Items[0]["id"].(string)

	body, _ := json.Marshal(map[string]string{"id": id, "action": "accept"})
	req, _ := http.NewRequest("POST", srv.URL+"/api/review/action", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Axon-Review", "1")
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res2.StatusCode != 200 {
		t.Fatalf("accept status = %d", res2.StatusCode)
	}
	n, _ := v.Read(t.Context(), "notes/a.md")
	if !strings.Contains(n.Body, "- [[notes/b]]") {
		t.Fatalf("link not applied:\n%s", n.Body)
	}
}

func TestReviewActionGuards(t *testing.T) {
	srv, _, _ := reviewTestServer(t)
	body := []byte(`{"id":"x","action":"accept"}`)

	// Missing the custom header → 403.
	req, _ := http.NewRequest("POST", srv.URL+"/api/review/action", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, _ := http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("no-header status = %d, want 403", res.StatusCode)
	}

	// Wrong content type → 403.
	req2, _ := http.NewRequest("POST", srv.URL+"/api/review/action", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "text/plain")
	req2.Header.Set("X-Axon-Review", "1")
	res2, _ := http.DefaultClient.Do(req2)
	if res2.StatusCode != http.StatusForbidden {
		t.Fatalf("bad-ct status = %d, want 403", res2.StatusCode)
	}

	// Unknown id → 4xx.
	req3, _ := http.NewRequest("POST", srv.URL+"/api/review/action", bytes.NewReader(body))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("X-Axon-Review", "1")
	res3, _ := http.DefaultClient.Do(req3)
	if res3.StatusCode < 400 || res3.StatusCode >= 500 {
		t.Fatalf("unknown-id status = %d, want 4xx", res3.StatusCode)
	}
}

func TestExportCSVAndJSON(t *testing.T) {
	srv, _, _ := reviewTestServer(t)
	res, err := http.Get(srv.URL + "/api/export?dataset=runs&format=csv")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("csv export: status %d ct %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, "axon-runs-") {
		t.Fatalf("disposition = %q", cd)
	}

	res2, _ := http.Get(srv.URL + "/api/export?dataset=tokens&format=json")
	if res2.StatusCode != 200 {
		t.Fatalf("json export status = %d", res2.StatusCode)
	}
	res3, _ := http.Get(srv.URL + "/api/export?dataset=bogus&format=csv")
	if res3.StatusCode != http.StatusBadRequest {
		t.Fatalf("bogus dataset status = %d, want 400", res3.StatusCode)
	}
}
