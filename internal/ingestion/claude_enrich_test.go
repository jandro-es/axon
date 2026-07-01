package ingestion

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/tokens"
)

// TestClaudeEnricherRoutesThroughManager proves the model-backed enricher's call
// is mediated by the token manager (cardinal rule 1) and ledgered (S4).
func TestClaudeEnricherRoutesThroughManager(t *testing.T) {
	ctx := context.Background()
	fake := agent.NewFake()
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{
			Text:  `Here you go: {"title":"Vector Search","summary":"A note about vectors.","tags":["search"],"links":["a.md"]}`,
			Model: r.Model,
			Usage: agent.Usage{InputTokens: 200, OutputTokens: 40},
		}, nil
	}
	limits := config.LimitsConfig{DailyTokens: 1_000_000, WeeklyTokens: 5_000_000}

	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	mgr := tokens.New(d, fake, nil, nil, tokens.Config{
		Profile: "test", AuthMode: "subscription",
		Models: config.ModelsConfig{Classify: "haiku", Routine: "sonnet", Synthesis: "opus"},
		Limits: limits,
	})

	enricher := ClaudeEnricher{Manager: mgr}
	enr, err := enricher.Enrich(ctx, EnrichInput{
		Title:    "raw title",
		Markdown: "Some article about vector search and embeddings.",
		Related:  []string{"a.md", "b.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if enr.Title != "Vector Search" || enr.Summary == "" {
		t.Errorf("parsed enrichment wrong: %+v", enr)
	}
	// Link suggestions are grounded: only candidate "a.md" is kept.
	if len(enr.SuggestedLinks) != 1 || enr.SuggestedLinks[0] != "a.md" {
		t.Errorf("links = %v, want [a.md]", enr.SuggestedLinks)
	}
	// S4: the model call was ledgered.
	if fake.CallCount() != 1 {
		t.Errorf("agent calls = %d, want 1", fake.CallCount())
	}
	if n, _ := db.CountLedger(ctx, d); n != 1 {
		t.Errorf("ledger rows = %d, want 1 (the enrichment call)", n)
	}
	// The enrichment carries its accounting so ingestion can surface token usage.
	if enr.Kind != "claude" || enr.Model != "sonnet" {
		t.Errorf("accounting kind/model = %q/%q, want claude/sonnet", enr.Kind, enr.Model)
	}
	if enr.InputTokens != 200 || enr.OutputTokens != 40 {
		t.Errorf("accounting tokens = in %d/out %d, want 200/40", enr.InputTokens, enr.OutputTokens)
	}
}

// TestClaudeEnricherDegradesUnderBudget proves a budget-denied enrichment falls
// back to deterministic heuristic output instead of failing ingestion.
func TestClaudeEnricherDegradesUnderBudget(t *testing.T) {
	ctx := context.Background()
	fake := agent.NewFake()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	// Tiny window so the cheapest-tier call is refused (defer/deny) -> fallback.
	mgr := tokens.New(d, fake, nil, nil, tokens.Config{
		Profile: "test", AuthMode: "subscription",
		Models: config.ModelsConfig{Classify: "haiku", Routine: "haiku", Synthesis: "haiku"},
		Limits: config.LimitsConfig{DailyTokens: 1, WeeklyTokens: 1},
	})

	enricher := ClaudeEnricher{Manager: mgr, ModelKey: "classify"}
	enr, err := enricher.Enrich(ctx, EnrichInput{
		Title:    "Fallback Title",
		Markdown: "First sentence here. Second sentence follows.",
		Related:  []string{"x.md"},
	})
	if err != nil {
		t.Fatalf("enrich should degrade gracefully, got error: %v", err)
	}
	// Heuristic fallback: title preserved, summary derived locally.
	if enr.Title != "Fallback Title" || enr.Summary == "" {
		t.Errorf("expected heuristic fallback enrichment, got %+v", enr)
	}
	if n, _ := db.CountLedger(ctx, d); n != 0 {
		t.Errorf("denied call should not ledger usage, got %d rows", n)
	}
}

func TestParseEnrichmentGroundsLinks(t *testing.T) {
	enr, err := parseEnrichment(
		`{"title":"T","summary":"S","tags":["a"],"links":["real.md","invented.md"]}`,
		[]string{"real.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(enr.SuggestedLinks) != 1 || enr.SuggestedLinks[0] != "real.md" {
		t.Errorf("links = %v, want only the grounded real.md", enr.SuggestedLinks)
	}
}
