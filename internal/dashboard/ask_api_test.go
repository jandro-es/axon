package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/vault"
)

func askTestServer(t *testing.T, enabled bool) *httptest.Server {
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
	for rel, body := range map[string]string{
		"Notes/vectors.md": "# Vector Databases\n\nVector databases index embeddings for similarity search.\n",
		"Notes/f1.md":      "# Gardening\n\nTomatoes need full sun.\n",
		"Notes/f2.md":      "# Cooking\n\nBraising renders collagen.\n",
		"Notes/f3.md":      "# Travel\n\nShoulder season is cheaper.\n",
	} {
		p := filepath.Join(v.Root(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := core.Reindex(context.Background(), v, d); err != nil {
		t.Fatal(err)
	}
	fake := agent.NewFake()
	fake.Reply = "Vector databases index embeddings for similarity search [[Notes/vectors]]."
	searcher := search.New(d, embeddings.NewFake())
	mgr := tokens.New(d, fake, searcher, nil, tokens.Config{
		Profile: "test", AuthMode: "subscription",
		Models: config.ModelsConfig{Classify: "haiku", Routine: "sonnet", Synthesis: "opus"},
		Limits: config.LimitsConfig{DailyTokens: 1_000_000, WeeklyTokens: 5_000_000, GuardPauseAtPct: 80},
	})
	s := New(Config{
		Profile: "test", DB: d, Manager: mgr, Searcher: searcher,
		Retrieval:  config.RetrievalConfig{TopK: 8, MaxContextTokens: 12_000},
		AskEnabled: enabled,
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func postAskReq(t *testing.T, url, question string, withHeader bool) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"question": question})
	req, _ := http.NewRequest("POST", url+"/api/ask", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if withHeader {
		req.Header.Set("X-Axon-Ask", "1")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAskAPIHappyPath(t *testing.T) {
	srv := askTestServer(t, true)
	res := postAskReq(t, srv.URL, "what are vector databases for similarity search", true)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var a struct {
		Refused   bool     `json:"refused"`
		Citations []string `json:"citations"`
	}
	if err := json.NewDecoder(res.Body).Decode(&a); err != nil {
		t.Fatal(err)
	}
	if a.Refused || len(a.Citations) != 1 {
		t.Fatalf("answer = %+v", a)
	}
}

func TestAskAPIGuardAndToggle(t *testing.T) {
	srv := askTestServer(t, true)
	if res := postAskReq(t, srv.URL, "q", false); res.StatusCode != http.StatusForbidden {
		t.Fatalf("no-header status = %d, want 403", res.StatusCode)
	}
	if res := postAskReq(t, srv.URL, "   ", true); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty-question status = %d, want 400", res.StatusCode)
	}
	off := askTestServer(t, false)
	if res := postAskReq(t, off.URL, "q", true); res.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled status = %d, want 404", res.StatusCode)
	}
}
