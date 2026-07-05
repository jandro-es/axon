package ask

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// newAskDeps wires the standard fakes: temp vault, in-memory DB (indexed via
// core.Reindex — lexical-only, vectors pending, exactly like a fresh install
// without Ollama), fake agent behind a real token manager.
func newAskDeps(t *testing.T, files map[string]string) (Deps, *agent.Fake, *sql.DB) {
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
	if _, err := core.Reindex(context.Background(), v, d); err != nil {
		t.Fatal(err)
	}
	fake := agent.NewFake()
	emb := embeddings.NewFake()
	searcher := search.New(d, emb)
	profile := config.Profile{
		Models:    config.ModelsConfig{Classify: "haiku", Routine: "sonnet", Synthesis: "opus"},
		Limits:    config.LimitsConfig{DailyTokens: 1_000_000, WeeklyTokens: 5_000_000, GuardPauseAtPct: 80},
		Retrieval: config.RetrievalConfig{TopK: 8, MaxContextTokens: 12_000},
	}
	mgr := tokens.New(d, fake, searcher, nil, tokens.Config{
		Profile: "test", AuthMode: "subscription", Models: profile.Models, Limits: profile.Limits,
	})
	return Deps{Searcher: searcher, Manager: mgr, Config: profile}, fake, d
}

// corpus seeds one target note plus filler: FTS5's bm25 IDF is near-zero in a
// single-document index, so filler notes make a genuine lexical match score
// with realistic strength (as in any real vault).
var corpus = map[string]string{
	"Notes/vectors.md": "# Vector Databases\n\nVector databases index embeddings for similarity search and hybrid retrieval.\n",
	"Notes/f1.md":      "# Gardening\n\nTomatoes need full sun and regular watering through summer.\n",
	"Notes/f2.md":      "# Cooking\n\nSlow braising tough cuts renders collagen into gelatin.\n",
	"Notes/f3.md":      "# Travel\n\nShoulder season flights cost less and queues are shorter.\n",
	"Notes/f4.md":      "# Music\n\nPractice scales slowly with a metronome before increasing tempo.\n",
	"Notes/f5.md":      "# Fitness\n\nProgressive overload drives strength adaptation over weeks.\n",
}

func TestGroundedGate(t *testing.T) {
	for _, tt := range []struct {
		name string
		hits []db.ChunkHit
		want bool
	}{
		{"no hits", nil, false},
		{"strong lexical match", []db.ChunkHit{{Path: "a.md", Lexical: -5.2}}, true},
		{"stop-word lexical noise", []db.ChunkHit{{Path: "a.md", Lexical: -1.9}}, false},
		{"semantic above floor", []db.ChunkHit{{Path: "a.md", Vector: 0.55}}, true},
		{"semantic in the unrelated band", []db.ChunkHit{{Path: "a.md", Vector: 0.40}}, false},
		{"semantic below floor", []db.ChunkHit{{Path: "a.md", Vector: 0.10}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := grounded(tt.hits); got != tt.want {
				t.Fatalf("grounded(%s) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestAskRefusesEmptyVaultWithoutModelCall(t *testing.T) {
	d, fake, _ := newAskDeps(t, nil)
	a, err := Ask(context.Background(), d, "what is a vector database?", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Refused || !strings.Contains(a.Reason, "nothing relevant") {
		t.Fatalf("answer = %+v", a)
	}
	if fake.CallCount() != 0 {
		t.Fatalf("gate must spend zero tokens, got %d call(s)", fake.CallCount())
	}
}

func TestAskHappyPathWithValidCitation(t *testing.T) {
	d, fake, _ := newAskDeps(t, corpus)
	fake.Reply = "Vector databases index embeddings for similarity search [[Notes/vectors]]."
	a, err := Ask(context.Background(), d, "vector databases embeddings similarity", 0)
	if err != nil {
		t.Fatal(err)
	}
	if a.Refused {
		t.Fatalf("refused: %+v", a)
	}
	if len(a.Citations) != 1 || a.Citations[0] != "Notes/vectors.md" {
		t.Fatalf("citations = %v", a.Citations)
	}
	if fake.CallCount() != 1 {
		t.Fatalf("want exactly one model call, got %d", fake.CallCount())
	}
}

func TestAskRejectsHallucinatedCitation(t *testing.T) {
	d, fake, _ := newAskDeps(t, corpus)
	fake.Reply = "Databases are cool [[Made/up-note]]."
	a, err := Ask(context.Background(), d, "vector databases embeddings similarity", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Refused || !strings.Contains(a.Reason, "citation") {
		t.Fatalf("hallucinated citation must refuse: %+v", a)
	}
	if len(a.Sources) == 0 {
		t.Fatal("refusal must list the retrieved sources")
	}
}

func TestAskRejectsZeroCitations(t *testing.T) {
	d, fake, _ := newAskDeps(t, corpus)
	fake.Reply = "Vector databases index embeddings."
	a, err := Ask(context.Background(), d, "vector databases embeddings similarity", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Refused || !strings.Contains(a.Reason, "citation") {
		t.Fatalf("uncited answer must refuse: %+v", a)
	}
}

func TestAskNotFoundIsGroundedRefusal(t *testing.T) {
	d, fake, _ := newAskDeps(t, corpus)
	fake.Reply = "NOT_FOUND"
	a, err := Ask(context.Background(), d, "vector databases embeddings similarity", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Refused || !strings.Contains(a.Reason, "don't answer") {
		t.Fatalf("NOT_FOUND must be a grounded refusal: %+v", a)
	}
	if fake.CallCount() != 1 {
		t.Fatalf("NOT_FOUND still costs one call, got %d", fake.CallCount())
	}
}

// deferringManager stubs tokens.Manager to exercise the budget branch
// directly: the real manager's downgrade ladder (FR-43) means an over-window
// synthesis call is downgraded rather than deferred, so a genuine defer/deny
// (exhausted cheapest tier, per-call caps) is simulated here.
type deferringManager struct{ tokens.Manager }

func (deferringManager) Run(context.Context, tokens.AgentCall) (tokens.AgentResult, error) {
	return tokens.AgentResult{}, fmt.Errorf("stub: %w", tokens.ErrDeferred)
}

func TestAskBudgetDefer(t *testing.T) {
	d, fake, _ := newAskDeps(t, corpus)
	d.Manager = deferringManager{}
	a, err := Ask(context.Background(), d, "vector databases embeddings similarity", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Refused || a.Reason != "budget" {
		t.Fatalf("deferred call must refuse with reason budget: %+v", a)
	}
	if fake.CallCount() != 0 {
		t.Fatal("deferred call must not reach the agent")
	}
}

// TestAskOverWindowDowngradesAndStillAnswers documents the systemwide FR-43
// policy applied to ask: starved day/week windows downgrade the tier and the
// question is still answered (cheaper), rather than refused.
func TestAskOverWindowDowngradesAndStillAnswers(t *testing.T) {
	d, fake, sqlDB := newAskDeps(t, corpus)
	starved := config.LimitsConfig{DailyTokens: 1, WeeklyTokens: 1, GuardPauseAtPct: 80}
	d.Manager = tokens.New(sqlDB, fake, d.Searcher, nil, tokens.Config{
		Profile: "test", AuthMode: "subscription", Models: d.Config.Models, Limits: starved,
	})
	fake.Reply = "Vector databases index embeddings [[Notes/vectors]]."
	a, err := Ask(context.Background(), d, "vector databases embeddings similarity", 0)
	if err != nil {
		t.Fatal(err)
	}
	if a.Refused {
		t.Fatalf("over-window should downgrade, not refuse: %+v", a)
	}
	if fake.CallCount() != 1 {
		t.Fatalf("downgraded call must still run once, got %d", fake.CallCount())
	}
}
