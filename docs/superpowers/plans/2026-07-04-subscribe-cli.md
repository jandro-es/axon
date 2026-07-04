# `axon subscribe` CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `axon subscribe <url>` / `list` / `remove <url>` manage `subscriptions.feeds` through the comment-preserving config editor, with verified adds and explicit egress-policy opt-in (FR-100, ADR-019 follow-up).

**Architecture:** One new cobra command file in `cmd/axon` composing existing seams: the block-rebuild config editor pattern (`setAutomationEnabled`), `ingestion.CheckIngestPolicy` + `ingestion.NewHTTPFetcher` for policy/verification, gofeed for parsing, `db.GetCursor/SetCursor` for the `subscriptions:seen` state. No new packages, no model calls, no vault writes.

**Tech Stack:** Go 1.26, cobra, goccy/go-yaml (`parser.ParseComments`, `yaml.PathString`, `ReplaceWithReader`/`MergeFromReader`), mmcdole/gofeed, modernc SQLite via `internal/db`.

## Global Constraints

- Go 1.26; `gofmt`/`goimports` clean, `go vet` and `golangci-lint run` green.
- No model calls, no ledger entries, no vault writes — pure config tooling (spec "What this is not").
- Every config write: rebuild block → `yaml.Marshal` → path replace/merge → re-`config.Parse` gate → `writeFileAtomic`. Comments outside the rewritten block must survive.
- The real `NewHTTPFetcher` refuses loopback dials (SSRF guard) — tests MUST go through the overridable `newSubscribeFetcher` seam, never httptest against the real fetcher.
- Verify test suites with `go test ./... > /dev/null 2>&1; echo $?` (piping to `tail` masks the exit code).
- Existing helpers reused verbatim (already in `cmd/axon`): `jsonPathFor(profile, key)` (`cmd/axon/config_cmd.go:33`), `writeFileAtomic` (`config_cmd.go:197`), `run(t, args...)` (`cli_test.go:20`), `writeTempConfig(t, dir)` (`init_cmd_test.go:14`).

---

### Task 1: `axon subscribe <url>` — verified add through the config editor

**Files:**
- Create: `cmd/axon/subscribe_cmd.go`
- Create: `cmd/axon/subscribe_cmd_test.go`
- Modify: `cmd/axon/root.go:38` (register the command)

**Interfaces:**
- Consumes: `config.Load/Parse`, `(*config.Config).ResolveProfile/ResolveProfileName`, `config.SubscriptionsConfig{Enrich, MaxPerTick, Feeds []config.Feed}`, `config.Feed{URL string}`, `ingestion.Fetcher` interface + `ingestion.NewHTTPFetcher(config.PolicyConfig) *HTTPFetcher` + `ingestion.Document{Body []byte}`, `gofeed.NewParser().Parse(io.Reader)`, `jsonPathFor`, `writeFileAtomic`.
- Produces (later tasks rely on): `newSubscribeCmd(gf *globalFlags) *cobra.Command`; `var newSubscribeFetcher func(config.PolicyConfig) ingestion.Fetcher` (test seam); `updateSubscriptionsBlock(gf *globalFlags, mutate func(*config.SubscriptionsConfig) error) error`; `replaceProfileBlock(configPath string, raw []byte, profileName, key string, block any) error`; test helper `writeSubscribeConfig(t *testing.T, dir string, extra string) string`.

- [ ] **Step 1: Write the failing tests** — `cmd/axon/subscribe_cmd_test.go`:

```go
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

```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/axon/ -run TestSubscribe 2>&1 | tail -3`
Expected: FAIL (build error: `newSubscribeFetcher`, `updateSubscriptionsBlock` undefined)

- [ ] **Step 3: Implement** — `cmd/axon/subscribe_cmd.go`:

```go
package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"
	"github.com/mmcdole/gofeed"
	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/ingestion"
)

// newSubscribeFetcher builds the egress-policied fetcher used to verify a
// feed at add time. A package variable so tests can stub it: the real
// fetcher's SSRF guard refuses loopback dials, which rules out httptest.
var newSubscribeFetcher = func(p config.PolicyConfig) ingestion.Fetcher {
	return ingestion.NewHTTPFetcher(p)
}

func newSubscribeCmd(gf *globalFlags) *cobra.Command {
	var noVerify, allow bool
	cmd := &cobra.Command{
		Use:   "subscribe <feed-url>",
		Short: "Subscribe to an RSS/Atom feed (polled by the subscriptions automation)",
		Long: "Verify a feed URL and append it to subscriptions.feeds in config.yaml\n" +
			"through the comment-preserving editor (FR-100, ADR-019). The feed is\n" +
			"fetched through the egress-policied fetcher and parsed before anything\n" +
			"is written; a host outside the ingest policy is refused unless --allow\n" +
			"opts it into policy.ingest_domains_allow explicitly.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return subscribeAdd(cmd, gf, args[0], noVerify, allow)
		},
	}
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "skip fetching the URL to verify it parses as a feed")
	cmd.Flags().BoolVar(&allow, "allow", false, "add the feed's host to policy.ingest_domains_allow if the policy refuses it")
	return cmd
}

func subscribeAdd(cmd *cobra.Command, gf *globalFlags, feedURL string, noVerify, allow bool) error {
	out := cmd.OutOrStdout()

	// 1. URL shape (mirrors validateSubscriptions): http(s) + host.
	u, err := url.Parse(feedURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("feed URL must be http(s) with a host (got %q)", feedURL)
	}

	cfg, err := config.Load(gf.configPath)
	if err != nil {
		return err
	}
	_, profile, err := cfg.ResolveProfile(gf.profile)
	if err != nil {
		return err
	}

	// 2. Duplicate: friendly no-op.
	for _, f := range profile.Subscriptions.Feeds {
		if f.URL == feedURL {
			fmt.Fprintf(out, "already subscribed: %s\n", feedURL)
			return nil
		}
	}

	// 3. Verification (default on): fetch through the egress-policied
	// fetcher, parse as a feed, report the title. NFR-04: same policy
	// enforcement as every ingest.
	if noVerify {
		fmt.Fprintln(out, "· verification skipped (--no-verify)")
	} else {
		doc, ferr := newSubscribeFetcher(profile.Policy).Fetch(cmd.Context(), feedURL)
		if ferr != nil {
			return fmt.Errorf("could not fetch feed (re-run with --no-verify to add anyway): %w", ferr)
		}
		parsed, perr := gofeed.NewParser().Parse(bytes.NewReader(doc.Body))
		if perr != nil {
			return fmt.Errorf("%s does not parse as RSS/Atom/JSON Feed (re-run with --no-verify to add anyway): %w", feedURL, perr)
		}
		fmt.Fprintf(out, "✓ verified: %q (%d entries)\n", parsed.Title, len(parsed.Items))
	}

	// 4. Append through the comment-preserving editor.
	if err := updateSubscriptionsBlock(gf, func(s *config.SubscriptionsConfig) error {
		s.Feeds = append(s.Feeds, config.Feed{URL: feedURL})
		return nil
	}); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ subscribed: %s\n", feedURL)
	fmt.Fprintln(out, "  Polled hourly by the subscriptions automation; the first poll marks existing entries seen (FR-92).")
	fmt.Fprintln(out, "  A running daemon applies this on restart — or run `axon run subscriptions` now.")
	return nil
}

// updateSubscriptionsBlock applies mutate to the active profile's
// subscriptions config and writes the block back (setAutomationEnabled
// pattern): rebuild from the parsed struct, re-render, path-replace. The
// subscriptions block becomes CLI-managed; comments inside it are rewritten,
// comments everywhere else survive.
func updateSubscriptionsBlock(gf *globalFlags, mutate func(*config.SubscriptionsConfig) error) error {
	raw, err := os.ReadFile(gf.configPath)
	if err != nil {
		return err
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		return err
	}
	name := cfg.ResolveProfileName(gf.profile)
	sub := cfg.Profiles[name].Subscriptions
	if err := mutate(&sub); err != nil {
		return err
	}
	return replaceProfileBlock(gf.configPath, raw, name, "subscriptions", sub)
}

// replaceProfileBlock rewrites one block under profiles.<name>
// comment-preservingly, creating the key when the profile doesn't have it
// yet. Every write is re-validated (config.Parse) and atomic.
func replaceProfileBlock(configPath string, raw []byte, profileName, key string, block any) error {
	file, err := parser.ParseBytes(raw, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	blockPath, err := yaml.PathString(jsonPathFor(profileName, key))
	if err != nil {
		return err
	}
	if _, rerr := blockPath.ReadNode(bytes.NewReader(raw)); rerr == nil {
		// Key exists: replace its value node.
		inner, merr := yaml.Marshal(block)
		if merr != nil {
			return merr
		}
		if err := blockPath.ReplaceWithReader(file, bytes.NewReader(inner)); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
	} else {
		// Key absent: merge {key: block} into the profile map.
		rendered, merr := yaml.Marshal(map[string]any{key: block})
		if merr != nil {
			return merr
		}
		parentPath, perr := yaml.PathString("$.profiles." + profileName)
		if perr != nil {
			return perr
		}
		if err := parentPath.MergeFromReader(file, bytes.NewReader(rendered)); err != nil {
			return fmt.Errorf("add %s: %w", key, err)
		}
	}
	updated := []byte(file.String())
	if _, err := config.Parse(updated); err != nil {
		return fmt.Errorf("refusing to write: the change makes the config invalid: %w", err)
	}
	return writeFileAtomic(configPath, updated)
}
```

- [ ] **Step 4: Register** — in `cmd/axon/root.go`, change line 38:

```go
	root.AddCommand(newIngestCmd(gf), newSearchCmd(gf), newStatusCmd(gf), newSubscribeCmd(gf))
```

- [ ] **Step 5: Run to verify pass**

Run: `go test ./cmd/axon/ -run 'TestSubscribe|TestReplaceProfileBlock' -v 2>&1 | tail -15 && go test ./... > /dev/null 2>&1; echo $?`
Expected: all PASS, suite exit 0. If `MergeFromReader` misbehaves on the absent-key path (indentation or unsupported merge), fall back to reading the profile node, appending a rendered `subscriptions:` mapping value via `blockPath` on the parent — but first trust the test: `TestSubscribeAddVerifiesAndCreatesBlock` exercises exactly this path.

- [ ] **Step 6: Commit**

```bash
git add cmd/axon/subscribe_cmd.go cmd/axon/subscribe_cmd_test.go cmd/axon/root.go
git commit -m "feat(cli): axon subscribe — verified feed add via comment-preserving editor (FR-100)"
```

---

### Task 2: Egress-policy refusal + `--allow`

**Files:**
- Modify: `cmd/axon/subscribe_cmd.go` (insert policy step into `subscribeAdd`, add `appendAllowDomain`)
- Test: `cmd/axon/subscribe_cmd_test.go` (append)

**Interfaces:**
- Consumes: `ingestion.CheckIngestPolicy(config.PolicyConfig, host string) error`; `replaceProfileBlock` from Task 1; `config.PolicyConfig.IngestDomainsAllow []string`.
- Produces: `appendAllowDomain(gf *globalFlags, host string) error`.

- [ ] **Step 1: Write the failing tests** (append to `subscribe_cmd_test.go`):

```go
// writeStrictConfig tightens the wildcard allowlist to one named host, so
// any other host is refused by CheckIngestPolicy.
func writeStrictConfig(t *testing.T, dir string) string {
	t.Helper()
	path := writeSubscribeConfig(t, dir, "")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	strict := strings.Replace(string(raw),
		`ingest_domains_allow: ["*"]`,
		`ingest_domains_allow: ["allowed.example.com"]`, 1)
	if strict == string(raw) {
		t.Fatal("policy line not found to tighten")
	}
	if err := os.WriteFile(path, []byte(strict), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSubscribePolicyRefusalAndAllow(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeStrictConfig(t, dir)
	stubFetcher(t, map[string]string{"https://feeds.example.com/a.xml": atomFixture})

	// Refused without --allow: non-zero, names the host, hints --allow,
	// config untouched.
	out, err := run(t, "subscribe", "https://feeds.example.com/a.xml", "--config", cfgPath)
	if err == nil {
		t.Fatalf("policy refusal must be an error:\n%s", out)
	}
	if !strings.Contains(err.Error(), "feeds.example.com") || !strings.Contains(err.Error(), "--allow") {
		t.Errorf("refusal must name host + hint --allow: %v", err)
	}
	if feeds := loadFeeds(t, cfgPath); len(feeds) != 0 {
		t.Fatalf("refused add wrote config: %+v", feeds)
	}

	// --allow: domain appended to the policy, feed subscribed.
	out, err = run(t, "subscribe", "https://feeds.example.com/a.xml", "--allow", "--config", cfgPath)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cfg, _ := config.Load(cfgPath)
	_, p, _ := cfg.ResolveProfile("")
	if !slicesContains(p.Policy.IngestDomainsAllow, "feeds.example.com") {
		t.Errorf("domain not appended: %v", p.Policy.IngestDomainsAllow)
	}
	if feeds := loadFeeds(t, cfgPath); len(feeds) != 1 {
		t.Fatalf("feeds = %+v", feeds)
	}
	raw, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(raw), "# keep-me") {
		t.Error("comment lost by policy rewrite")
	}
}

func TestSubscribeAllowedHostMakesNoPolicyEdit(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSubscribeConfig(t, dir, "") // wildcard policy: check passes
	stubFetcher(t, map[string]string{"https://feeds.example.com/a.xml": atomFixture})

	if out, err := run(t, "subscribe", "https://feeds.example.com/a.xml", "--allow", "--config", cfgPath); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cfg, _ := config.Load(cfgPath)
	_, p, _ := cfg.ResolveProfile("")
	// The flag only acts on refusal: wildcard list untouched.
	if len(p.Policy.IngestDomainsAllow) != 1 || p.Policy.IngestDomainsAllow[0] != "*" {
		t.Errorf("policy edited without need: %v", p.Policy.IngestDomainsAllow)
	}
}

func slicesContains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
```


- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/axon/ -run TestSubscribePolicy 2>&1 | tail -3`
Expected: FAIL (`policy refusal must be an error` — no policy check yet)

- [ ] **Step 3: Implement.** In `subscribeAdd`, insert between the duplicate check (step 2) and verification (step 3):

```go
	// 3. Ingest policy: refusal is explicit; --allow opts the domain in
	// (never silent — policy changes are user consent).
	host := u.Hostname()
	if perr := ingestion.CheckIngestPolicy(profile.Policy, host); perr != nil {
		if !allow {
			return fmt.Errorf("host %q is refused by the ingest policy (%v)\n"+
				"  add %q to policy.ingest_domains_allow in %s, or re-run with --allow",
				host, perr, host, gf.configPath)
		}
		if err := appendAllowDomain(gf, host); err != nil {
			return err
		}
		fmt.Fprintf(out, "✓ added %q to policy.ingest_domains_allow\n", host)
		// Re-load so the verification fetch runs under the updated policy.
		cfg, err = config.Load(gf.configPath)
		if err != nil {
			return err
		}
		if _, profile, err = cfg.ResolveProfile(gf.profile); err != nil {
			return err
		}
	}
```

And add at the bottom of the file:

```go
// appendAllowDomain appends host to the profile's ingest_domains_allow via
// the same block-rebuild editor (the policy block becomes CLI-managed too).
func appendAllowDomain(gf *globalFlags, host string) error {
	raw, err := os.ReadFile(gf.configPath)
	if err != nil {
		return err
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		return err
	}
	name := cfg.ResolveProfileName(gf.profile)
	pol := cfg.Profiles[name].Policy
	pol.IngestDomainsAllow = append(pol.IngestDomainsAllow, host)
	return replaceProfileBlock(gf.configPath, raw, name, "policy", pol)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./cmd/axon/ -run 'TestSubscribe' -v 2>&1 | tail -12 && go test ./... > /dev/null 2>&1; echo $?`
Expected: all PASS, exit 0.

- [ ] **Step 5: Commit**

```bash
git add cmd/axon/subscribe_cmd.go cmd/axon/subscribe_cmd_test.go
git commit -m "feat(cli): subscribe policy gate — explicit refusal, --allow opt-in (FR-100)"
```

---

### Task 3: `axon subscribe list`

**Files:**
- Modify: `cmd/axon/subscribe_cmd.go` (add subcommand)
- Test: `cmd/axon/subscribe_cmd_test.go` (append)

**Interfaces:**
- Consumes: `db.Open(path) (*sql.DB, error)`, `db.Migrate(*sql.DB)`, `db.GetCursor(ctx, q, "subscriptions:seen") (string, error)`; seen-state JSON shape `map[feedURL][]itemURL` (written by `internal/automations/subscriptions.go`); `config.Profile.Paths().DBPath`.
- Produces: `newSubscribeListCmd(gf *globalFlags) *cobra.Command`; `loadSeenState(ctx, profile) map[string][]string`; test helper `seedSeenState(t, cfgPath, jsonValue string)`.

- [ ] **Step 1: Write the failing tests** (append):

```go
func seedSeenState(t *testing.T, cfgPath, jsonValue string) {
	t.Helper()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := cfg.ResolveProfile("")
	if err != nil {
		t.Fatal(err)
	}
	paths := p.Paths()
	if err := os.MkdirAll(filepath.Dir(paths.DBPath), 0o755); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(paths.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err := db.Migrate(sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := db.SetCursor(context.Background(), sqlDB, "subscriptions:seen", jsonValue, "2026-07-04T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
}

func TestSubscribeList(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSubscribeConfig(t, dir, "")
	stubFetcher(t, nil)
	for _, u := range []string{"https://feeds.example.com/a.xml", "https://feeds.example.com/b.xml"} {
		if _, err := run(t, "subscribe", u, "--no-verify", "--config", cfgPath); err != nil {
			t.Fatal(err)
		}
	}
	seedSeenState(t, cfgPath, `{"https://feeds.example.com/a.xml":["u1","u2","u3"]}`)

	out, err := run(t, "subscribe", "list", "--config", cfgPath)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "a.xml") || !strings.Contains(out, "3 entr") {
		t.Errorf("polled feed must show its seen count:\n%s", out)
	}
	if !strings.Contains(out, "b.xml") || !strings.Contains(out, "pending first poll") {
		t.Errorf("unpolled feed must show pending:\n%s", out)
	}
}

func TestSubscribeListDegradesWithoutDB(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSubscribeConfig(t, dir, "")
	stubFetcher(t, nil)
	if _, err := run(t, "subscribe", "https://feeds.example.com/a.xml", "--no-verify", "--config", cfgPath); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "subscribe", "list", "--config", cfgPath) // data dir never created
	if err != nil {
		t.Fatalf("list must not error without a DB: %v", err)
	}
	if !strings.Contains(out, "pending first poll") {
		t.Errorf("out = %q", out)
	}
}

func TestSubscribeListEmpty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSubscribeConfig(t, dir, "")
	out, err := run(t, "subscribe", "list", "--config", cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no feeds subscribed") {
		t.Errorf("out = %q", out)
	}
}
```

Add `"github.com/jandro-es/axon/internal/db"` to the test imports.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/axon/ -run TestSubscribeList 2>&1 | tail -3`
Expected: FAIL (`unknown command "list"` surfaces as the add-path URL-shape error)

- [ ] **Step 3: Implement.** In `newSubscribeCmd`, before `return cmd` add:

```go
	cmd.AddCommand(newSubscribeListCmd(gf))
```

And append to `subscribe_cmd.go` (imports gain `"context"`, `"encoding/json"`, `"github.com/jandro-es/axon/internal/db"`):

```go
func newSubscribeListCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List subscribed feeds and their poll state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			cfg, err := config.Load(gf.configPath)
			if err != nil {
				return err
			}
			_, profile, err := cfg.ResolveProfile(gf.profile)
			if err != nil {
				return err
			}
			feeds := profile.Subscriptions.Feeds
			if len(feeds) == 0 {
				fmt.Fprintln(out, "no feeds subscribed — add one with `axon subscribe <url>`")
				return nil
			}
			seen := loadSeenState(cmd.Context(), profile)
			for _, f := range feeds {
				state := "pending first poll"
				if items, ok := seen[f.URL]; ok {
					state = fmt.Sprintf("%d entr(ies) seen", len(items))
				}
				fmt.Fprintf(out, "%s — %s\n", f.URL, state)
			}
			return nil
		},
	}
}

// loadSeenState reads the subscriptions automation's seen-state row
// ("subscriptions:seen", map[feedURL][]itemURL). Any failure — no data dir,
// no DB, no row, bad JSON — degrades to an empty map; list/remove never
// error on state.
func loadSeenState(ctx context.Context, profile config.Profile) map[string][]string {
	seen := map[string][]string{}
	dbPath := profile.Paths().DBPath
	if _, err := os.Stat(dbPath); err != nil {
		return seen
	}
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		return seen
	}
	defer sqlDB.Close()
	raw, err := db.GetCursor(ctx, sqlDB, "subscriptions:seen")
	if err != nil || raw == "" {
		return seen
	}
	_ = json.Unmarshal([]byte(raw), &seen)
	return seen
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./cmd/axon/ -run TestSubscribeList -v 2>&1 | tail -8 && go test ./... > /dev/null 2>&1; echo $?`
Expected: 3 PASS, exit 0.

- [ ] **Step 5: Commit**

```bash
git add cmd/axon/subscribe_cmd.go cmd/axon/subscribe_cmd_test.go
git commit -m "feat(cli): subscribe list — feeds + seen-state, DB-optional (FR-100)"
```

---

### Task 4: `axon subscribe remove <url>`

**Files:**
- Modify: `cmd/axon/subscribe_cmd.go` (add subcommand)
- Test: `cmd/axon/subscribe_cmd_test.go` (append)

**Interfaces:**
- Consumes: `updateSubscriptionsBlock`, `loadSeenState` (Task 3), `db.SetCursor(ctx, q, automation, cursor, updated string) error`.
- Produces: `newSubscribeRemoveCmd(gf *globalFlags) *cobra.Command`.

- [ ] **Step 1: Write the failing tests** (append; imports gain `"time"` if not present — not needed in tests, only impl):

```go
func TestSubscribeRemoveDropsFeedAndSeenState(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSubscribeConfig(t, dir, "")
	stubFetcher(t, nil)
	for _, u := range []string{"https://feeds.example.com/a.xml", "https://feeds.example.com/b.xml"} {
		if _, err := run(t, "subscribe", u, "--no-verify", "--config", cfgPath); err != nil {
			t.Fatal(err)
		}
	}
	seedSeenState(t, cfgPath, `{"https://feeds.example.com/a.xml":["u1"],"https://feeds.example.com/b.xml":["u2"]}`)

	out, err := run(t, "subscribe", "remove", "https://feeds.example.com/a.xml", "--config", cfgPath)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	feeds := loadFeeds(t, cfgPath)
	if len(feeds) != 1 || feeds[0].URL != "https://feeds.example.com/b.xml" {
		t.Fatalf("feeds = %+v", feeds)
	}
	// Seen entry dropped so a re-subscribe re-baselines (subscribe-from-now).
	cfg, _ := config.Load(cfgPath)
	_, p, _ := cfg.ResolveProfile("")
	seen := loadSeenState(context.Background(), p)
	if _, ok := seen["https://feeds.example.com/a.xml"]; ok {
		t.Error("seen entry for removed feed must be dropped")
	}
	if _, ok := seen["https://feeds.example.com/b.xml"]; !ok {
		t.Error("other feeds' seen entries must survive")
	}
	raw, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(raw), "# keep-me") {
		t.Error("comment lost")
	}
}

func TestSubscribeRemoveUnknownURL(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSubscribeConfig(t, dir, "")
	stubFetcher(t, nil)
	if _, err := run(t, "subscribe", "https://feeds.example.com/a.xml", "--no-verify", "--config", cfgPath); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "subscribe", "remove", "https://feeds.example.com/nope.xml", "--config", cfgPath)
	if err == nil {
		t.Fatal("unknown URL must be an error")
	}
	if !strings.Contains(err.Error(), "a.xml") {
		t.Errorf("error must list current feeds: %v", err)
	}
	if feeds := loadFeeds(t, cfgPath); len(feeds) != 1 {
		t.Fatalf("config mutated: %+v", feeds)
	}
}

func TestSubscribeRemoveWithoutDB(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSubscribeConfig(t, dir, "")
	stubFetcher(t, nil)
	if _, err := run(t, "subscribe", "https://feeds.example.com/a.xml", "--no-verify", "--config", cfgPath); err != nil {
		t.Fatal(err)
	}
	// No data dir/DB: the config edit must still proceed.
	if out, err := run(t, "subscribe", "remove", "https://feeds.example.com/a.xml", "--config", cfgPath); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if feeds := loadFeeds(t, cfgPath); len(feeds) != 0 {
		t.Fatalf("feeds = %+v", feeds)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/axon/ -run TestSubscribeRemove 2>&1 | tail -3`
Expected: FAIL (`remove` not a subcommand — routed to add, "already subscribed"/URL errors)

- [ ] **Step 3: Implement.** In `newSubscribeCmd`, change the AddCommand line:

```go
	cmd.AddCommand(newSubscribeListCmd(gf), newSubscribeRemoveCmd(gf))
```

Append (imports gain `"strings"`, `"time"`):

```go
func newSubscribeRemoveCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <feed-url>",
		Short: "Unsubscribe a feed (drops its seen-state so re-subscribing re-baselines)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			feedURL := args[0]
			cfg, err := config.Load(gf.configPath)
			if err != nil {
				return err
			}
			_, profile, err := cfg.ResolveProfile(gf.profile)
			if err != nil {
				return err
			}
			found := false
			var current []string
			for _, f := range profile.Subscriptions.Feeds {
				current = append(current, "  "+f.URL)
				if f.URL == feedURL {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("not subscribed: %s\ncurrent feeds (exact URL match):\n%s",
					feedURL, strings.Join(current, "\n"))
			}
			if err := updateSubscriptionsBlock(gf, func(s *config.SubscriptionsConfig) error {
				kept := s.Feeds[:0]
				for _, f := range s.Feeds {
					if f.URL != feedURL {
						kept = append(kept, f)
					}
				}
				s.Feeds = kept
				return nil
			}); err != nil {
				return err
			}
			dropSeenEntry(cmd.Context(), profile, feedURL)
			fmt.Fprintf(out, "✓ unsubscribed: %s\n", feedURL)
			return nil
		},
	}
}

// dropSeenEntry removes the feed's seen-state so a later re-subscribe gets
// subscribe-from-now semantics again. Best-effort: a missing/unreadable DB
// never blocks the config edit (stale entries are harmless and capped).
func dropSeenEntry(ctx context.Context, profile config.Profile, feedURL string) {
	dbPath := profile.Paths().DBPath
	if _, err := os.Stat(dbPath); err != nil {
		return
	}
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		return
	}
	defer sqlDB.Close()
	raw, err := db.GetCursor(ctx, sqlDB, "subscriptions:seen")
	if err != nil || raw == "" {
		return
	}
	var seen map[string][]string
	if json.Unmarshal([]byte(raw), &seen) != nil {
		return
	}
	if _, ok := seen[feedURL]; !ok {
		return
	}
	delete(seen, feedURL)
	b, err := json.Marshal(seen)
	if err != nil {
		return
	}
	_ = db.SetCursor(ctx, sqlDB, "subscriptions:seen", string(b), time.Now().UTC().Format(time.RFC3339))
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./cmd/axon/ -run TestSubscribe -v 2>&1 | tail -20 && go test ./... > /dev/null 2>&1; echo $?`
Expected: all subscribe tests PASS, exit 0.

- [ ] **Step 5: Commit**

```bash
git add cmd/axon/subscribe_cmd.go cmd/axon/subscribe_cmd_test.go
git commit -m "feat(cli): subscribe remove — config row + seen-state re-baseline (FR-100)"
```

---

### Task 5: Docs, CHANGELOG, gates, live smoke

**Files:**
- Modify: `docs/03-requirements.md` (FR-100 row: drop the "planned" marker), `CHANGELOG.md`, `axon.config.example.yaml` (mention the CLI in the feeds comment)
- No code changes.

- [ ] **Step 1: Docs.** In `docs/03-requirements.md`, in the FR-100 row change `**Subscribe CLI** *(planned — spec approved 2026-07-04, not yet built)*.` to `**Subscribe CLI.**` (the rest of the row already describes the built behavior). In `axon.config.example.yaml`, change the line `# subscriptions:                          # RSS/Atom feeds polled hourly (ADR-019)` to `# subscriptions:                          # RSS/Atom feeds polled hourly (ADR-019); manage with axon subscribe`. In `CHANGELOG.md` under `### Added` (top of the list):

```markdown
- **`axon subscribe` CLI (FR-100)** — manage feed subscriptions without
  hand-editing config: `axon subscribe <url>` fetches the feed through the
  egress-policied fetcher, parses it (gofeed), and appends it to
  `subscriptions.feeds` via the comment-preserving editor with re-validation
  and an atomic write (`--no-verify` skips the fetch); a host outside the
  ingest policy is refused with guidance unless `--allow` explicitly opts it
  into `ingest_domains_allow`. `subscribe list` shows each feed's seen-state;
  `subscribe remove <url>` drops the feed and its seen entry so
  re-subscribing re-baselines (subscribe-from-now). Closes ADR-019's noted
  follow-up slice.
```

- [ ] **Step 2: Gates.**

Run: `go build ./... && go vet ./... && golangci-lint run && go test ./... > /dev/null 2>&1; echo $?`
Expected: `0 issues.`, exit 0.

- [ ] **Step 3: Live smoke** (scratch env at `/private/tmp/claude-501/-Users-jandro-Projects-axon/ee1556a6-d9b5-4a70-8538-3b43e2143ed6/scratchpad/capture-smoke`, config `home/config.yaml`, profile `scratch`):

```bash
SMOKE=/private/tmp/claude-501/-Users-jandro-Projects-axon/ee1556a6-d9b5-4a70-8538-3b43e2143ed6/scratchpad/capture-smoke
go build -o "$SMOKE/axon" ./cmd/axon
"$SMOKE/axon" subscribe https://xkcd.com/atom.xml --config "$SMOKE/home/config.yaml"        # real fetch+verify, expect title "xkcd.com"
"$SMOKE/axon" subscribe https://go.dev/blog/feed.atom --config "$SMOKE/home/config.yaml"    # already in config → "already subscribed"
"$SMOKE/axon" subscribe list --config "$SMOKE/home/config.yaml"                              # go.dev shows seen count (P5 smoke polled it), xkcd pending
"$SMOKE/axon" subscribe remove https://xkcd.com/atom.xml --config "$SMOKE/home/config.yaml" # row gone
grep -A4 "feeds:" "$SMOKE/home/config.yaml"                                                  # only go.dev remains; comments intact
```

Expected: verification prints the xkcd feed title + entry count; dedupe no-op on go.dev; list shows the states; after remove the config holds only the go.dev feed and the file's comments survive.

- [ ] **Step 4: Commit**

```bash
git add docs/03-requirements.md CHANGELOG.md axon.config.example.yaml
git commit -m "docs: FR-100 built — subscribe CLI docs + CHANGELOG"
```

---

## Verification (definition of done)

1. Gates green: build, vet, golangci-lint, full suite.
2. FR trace: FR-100 fully covered (add/verify/policy in Tasks 1-2, list Task 3, remove Task 4).
3. Cardinal rules untouched: no model call anywhere (no `tokens`/`agent` import in `subscribe_cmd.go`); no vault write.
4. Comment preservation asserted in every mutating test (`# keep-me`).
5. Live smoke proves the real fetcher path end-to-end against public feeds.
