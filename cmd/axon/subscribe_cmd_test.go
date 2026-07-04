package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/ingestion"
)

// writeSubscribeConfig writes a minimal valid config with NO subscriptions
// block, a marker comment (comment preservation is asserted), and optional
// extra profile lines (indented 4 spaces, e.g. a policy override).
func writeSubscribeConfig(t *testing.T, dir, extra string) string {
	t.Helper()
	cfg := `version: 1
project_name: axon
active_profile: personal
# keep-me: file-level comment that every edit must preserve
profiles:
  personal:
    vault_path: "` + filepath.ToSlash(filepath.Join(dir, "vault")) + `"
    data_dir: "` + filepath.ToSlash(filepath.Join(dir, "data")) + `"
    claude: { auth_mode: subscription }
    dashboard: { host: "127.0.0.1", port: 7777 }
    embeddings: { provider: ollama, host: "http://127.0.0.1:1", model: nomic-embed-text, dim: 768, batch_size: 32 }
    models: { classify: h, routine: s, synthesis: o }
    limits: { daily_tokens: 1_000_000, weekly_tokens: 5_000_000, guard_pause_at_pct: 80 }
    retrieval: { top_k: 8, max_context_tokens: 12_000 }
    policy: { data_residency: local-only, egress_allowlist: ["*"], ingest_domains_allow: ["*"], ingest_domains_deny: [], redaction_rules: [], allowed_automations: ["*"] }
    automations: {}
` + extra
	path := filepath.Join(dir, "axon.config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const atomFixture = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Example Feed</title>
  <id>urn:example</id>
  <updated>2026-07-04T00:00:00Z</updated>
  <entry><title>One</title><id>urn:1</id><link href="https://feeds.example.com/1"/><updated>2026-07-04T00:00:00Z</updated></entry>
  <entry><title>Two</title><id>urn:2</id><link href="https://feeds.example.com/2"/><updated>2026-07-04T00:00:00Z</updated></entry>
</feed>`

// countingFetcher wraps the ingestion fake and counts Fetch calls, so
// --no-verify can assert the network path was never taken.
type countingFetcher struct {
	inner *ingestion.Fake
	calls int
}

func (c *countingFetcher) Fetch(ctx context.Context, url string) (*ingestion.Document, error) {
	c.calls++
	return c.inner.Fetch(ctx, url)
}

// stubFetcher swaps the package fetcher seam for a canned fake; restored on
// cleanup. The real fetcher refuses loopback dials, so tests must stub.
func stubFetcher(t *testing.T, docs map[string]string) *countingFetcher {
	t.Helper()
	fake := ingestion.NewFake()
	for u, body := range docs {
		fake.Docs[u] = body
	}
	cf := &countingFetcher{inner: fake}
	prev := newSubscribeFetcher
	newSubscribeFetcher = func(config.PolicyConfig) ingestion.Fetcher { return cf }
	t.Cleanup(func() { newSubscribeFetcher = prev })
	return cf
}

func loadFeeds(t *testing.T, cfgPath string) []config.Feed {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := cfg.ResolveProfile("")
	if err != nil {
		t.Fatal(err)
	}
	return p.Subscriptions.Feeds
}

func TestSubscribeAddVerifiesAndCreatesBlock(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSubscribeConfig(t, dir, "")
	cf := stubFetcher(t, map[string]string{"https://feeds.example.com/a.xml": atomFixture})

	out, err := run(t, "subscribe", "https://feeds.example.com/a.xml", "--config", cfgPath)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "Example Feed") || !strings.Contains(out, "2 entries") {
		t.Errorf("verification must report title + entry count:\n%s", out)
	}
	if cf.calls != 1 {
		t.Errorf("fetch calls = %d, want 1", cf.calls)
	}
	feeds := loadFeeds(t, cfgPath)
	if len(feeds) != 1 || feeds[0].URL != "https://feeds.example.com/a.xml" {
		t.Fatalf("feeds = %+v", feeds)
	}
	raw, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(raw), "# keep-me") {
		t.Error("file-level comment lost")
	}
}

func TestSubscribeAddAppendsAndDedupes(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSubscribeConfig(t, dir, "")
	stubFetcher(t, map[string]string{
		"https://feeds.example.com/a.xml": atomFixture,
		"https://feeds.example.com/b.xml": atomFixture,
	})

	for _, u := range []string{"https://feeds.example.com/a.xml", "https://feeds.example.com/b.xml"} {
		if out, err := run(t, "subscribe", u, "--config", cfgPath); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	if feeds := loadFeeds(t, cfgPath); len(feeds) != 2 || feeds[0].URL != "https://feeds.example.com/a.xml" {
		t.Fatalf("append broke: %+v", feeds)
	}
	// Duplicate: friendly no-op, exit 0, config unchanged.
	out, err := run(t, "subscribe", "https://feeds.example.com/a.xml", "--config", cfgPath)
	if err != nil {
		t.Fatalf("dedupe must exit 0: %v", err)
	}
	if !strings.Contains(out, "already subscribed") {
		t.Errorf("out = %q", out)
	}
	if feeds := loadFeeds(t, cfgPath); len(feeds) != 2 {
		t.Fatalf("dedupe wrote anyway: %+v", feeds)
	}
}

func TestSubscribeAddRejectsBadURLAndBadFeed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSubscribeConfig(t, dir, "")
	stubFetcher(t, map[string]string{"https://feeds.example.com/page.html": "<html><body>not a feed</body></html>"})

	if _, err := run(t, "subscribe", "ftp://feeds.example.com/a.xml", "--config", cfgPath); err == nil {
		t.Error("non-http scheme must be rejected")
	}
	if _, err := run(t, "subscribe", "not-a-url", "--config", cfgPath); err == nil {
		t.Error("garbage URL must be rejected")
	}
	// Fetch OK but body is HTML: parse failure aborts, config untouched.
	out, err := run(t, "subscribe", "https://feeds.example.com/page.html", "--config", cfgPath)
	if err == nil {
		t.Fatalf("HTML body must fail verification:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--no-verify") {
		t.Errorf("error must hint at --no-verify: %v", err)
	}
	// Fetch itself fails (URL unknown to the fake).
	if _, err := run(t, "subscribe", "https://feeds.example.com/missing.xml", "--config", cfgPath); err == nil {
		t.Error("fetch failure must abort the add")
	}
	if feeds := loadFeeds(t, cfgPath); len(feeds) != 0 {
		t.Fatalf("failed adds wrote config: %+v", feeds)
	}
}

func TestSubscribeNoVerifySkipsFetch(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSubscribeConfig(t, dir, "")
	cf := stubFetcher(t, nil) // any fetch would error (no canned docs)

	out, err := run(t, "subscribe", "https://feeds.example.com/flaky.xml", "--no-verify", "--config", cfgPath)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if cf.calls != 0 {
		t.Errorf("--no-verify still fetched (%d calls)", cf.calls)
	}
	if feeds := loadFeeds(t, cfgPath); len(feeds) != 1 {
		t.Fatalf("feeds = %+v", feeds)
	}
}

func TestReplaceProfileBlockRefusesInvalidWrite(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSubscribeConfig(t, dir, "")
	gf := &globalFlags{configPath: cfgPath}
	err := updateSubscriptionsBlock(gf, func(s *config.SubscriptionsConfig) error {
		s.Feeds = append(s.Feeds, config.Feed{URL: "not-a-url"})
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to write") {
		t.Fatalf("invalid block must be refused before writing, got %v", err)
	}
	if feeds := loadFeeds(t, cfgPath); len(feeds) != 0 {
		t.Fatalf("invalid write landed: %+v", feeds)
	}
}
