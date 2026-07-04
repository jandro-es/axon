package automations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/ingestion"
)

// urlFetcher serves canned documents by exact URL and counts fetches.
type urlFetcher struct {
	docs    map[string]*ingestion.Document
	calls   map[string]int
	failAll bool
}

func newURLFetcher() *urlFetcher {
	return &urlFetcher{docs: map[string]*ingestion.Document{}, calls: map[string]int{}}
}

func (u *urlFetcher) Fetch(ctx context.Context, url string) (*ingestion.Document, error) {
	u.calls[url]++
	if u.failAll {
		return nil, errors.New("connection refused")
	}
	d, ok := u.docs[url]
	if !ok {
		return nil, fmt.Errorf("404 for %s", url)
	}
	return d, nil
}

func (u *urlFetcher) addHTML(url, title string) {
	u.docs[url] = &ingestion.Document{URL: url, ContentType: "text/html",
		Body: []byte("<html><head><title>" + title + "</title></head><body><article><p>Substantial article content for " + title + " with enough words to extract meaningfully and produce a note.</p></article></body></html>")}
}

func rssXML(items ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>Test Feed</title>`)
	for i, link := range items {
		fmt.Fprintf(&b, `<item><title>Item %d</title><link>%s</link><pubDate>Mon, 0%d Jun 2026 10:00:00 GMT</pubDate></item>`, i+1, link, (i%9)+1)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

const testFeedURL = "https://feeds.example.com/feed.xml"

func subsRC(t *testing.T, fetcher *urlFetcher, feedURLs ...string) RunCtx {
	t.Helper()
	rc, _ := newRC(t, nil)
	rc.Pipeline.Fetcher = fetcher
	feeds := make([]config.Feed, len(feedURLs))
	for i, u := range feedURLs {
		feeds[i] = config.Feed{URL: u}
	}
	rc.Config.Subscriptions = config.SubscriptionsConfig{Feeds: feeds}
	return rc
}

func TestSubscriptionsSubscribeFromNow(t *testing.T) {
	f := newURLFetcher()
	f.docs[testFeedURL] = &ingestion.Document{URL: testFeedURL, ContentType: "application/rss+xml",
		Body: []byte(rssXML("https://example.com/a1", "https://example.com/a2"))}
	rc := subsRC(t, f, testFeedURL)
	ctx := context.Background()

	// First tick: marks seen, ingests nothing.
	res, err := (Subscriptions{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "subscribed") {
		t.Fatalf("summary = %q, want subscribed marker", res.Summary)
	}
	if f.calls["https://example.com/a1"] != 0 {
		t.Fatal("first tick must not ingest existing entries")
	}

	// Second tick, new item appears: only IT is ingested.
	f.docs[testFeedURL].Body = []byte(rssXML("https://example.com/a1", "https://example.com/a2", "https://example.com/a3"))
	f.addHTML("https://example.com/a3", "Article Three")
	res2, err := (Subscriptions{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res2.Summary, "ingested 1") {
		t.Fatalf("summary = %q, want ingested 1", res2.Summary)
	}
	if f.calls["https://example.com/a1"] != 0 || f.calls["https://example.com/a3"] != 1 {
		t.Fatalf("fetch counts wrong: %v", f.calls)
	}
	// A knowledge note landed.
	paths, _ := rc.Vault.List(ctx)
	found := false
	for _, p := range paths {
		if strings.HasPrefix(p, "03-Resources/Knowledge/") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no knowledge note; vault: %v", paths)
	}

	// Third tick, nothing new: nothing ingested.
	res3, _ := (Subscriptions{}).Run(ctx, rc)
	if !strings.Contains(res3.Summary, "ingested 0") {
		t.Fatalf("summary = %q, want ingested 0", res3.Summary)
	}
}

func TestSubscriptionsPerTickCap(t *testing.T) {
	f := newURLFetcher()
	f.docs[testFeedURL] = &ingestion.Document{URL: testFeedURL,
		Body: []byte(rssXML("https://example.com/seed"))}
	rc := subsRC(t, f, testFeedURL)
	rc.Config.Subscriptions.MaxPerTick = 2
	ctx := context.Background()
	if _, err := (Subscriptions{}).Run(ctx, rc); err != nil { // subscribe tick
		t.Fatal(err)
	}

	// Four new items appear; cap 2 per tick.
	links := []string{"https://example.com/seed", "https://example.com/n1", "https://example.com/n2", "https://example.com/n3", "https://example.com/n4"}
	f.docs[testFeedURL].Body = []byte(rssXML(links...))
	for _, l := range links[1:] {
		f.addHTML(l, l)
	}
	res, err := (Subscriptions{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "ingested 2") {
		t.Fatalf("summary = %q, want ingested 2 (capped)", res.Summary)
	}
	// Next tick drains two more.
	res2, _ := (Subscriptions{}).Run(ctx, rc)
	if !strings.Contains(res2.Summary, "ingested 2") {
		t.Fatalf("drain summary = %q, want ingested 2", res2.Summary)
	}
}

func TestSubscriptionsMarkSeenAfterFailure(t *testing.T) {
	f := newURLFetcher()
	f.docs[testFeedURL] = &ingestion.Document{URL: testFeedURL,
		Body: []byte(rssXML("https://example.com/seed"))}
	rc := subsRC(t, f, testFeedURL)
	ctx := context.Background()
	if _, err := (Subscriptions{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	// New item whose page 404s (not in f.docs).
	f.docs[testFeedURL].Body = []byte(rssXML("https://example.com/seed", "https://example.com/broken"))
	res, err := (Subscriptions{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err) // per-item failure must not fail the run
	}
	if !strings.Contains(res.Summary, "1 failed") {
		t.Fatalf("summary = %q, want failure counted", res.Summary)
	}
	attempts := f.calls["https://example.com/broken"]

	// Next tick: NOT retried (mark-seen-after-attempt).
	if _, err := (Subscriptions{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if f.calls["https://example.com/broken"] != attempts {
		t.Fatal("failed item was retried")
	}
}

func TestSubscriptionsFeedFailureIsolated(t *testing.T) {
	good, bad := "https://good.example.com/feed.xml", "https://bad.example.com/feed.xml"
	f := newURLFetcher()
	f.docs[good] = &ingestion.Document{URL: good, Body: []byte(rssXML("https://example.com/g1"))}
	// bad feed: no doc → fetch error
	rc := subsRC(t, f, bad, good)
	res, err := (Subscriptions{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("one bad feed must not fail the run: %v", err)
	}
	if !strings.Contains(res.Summary, "subscribed") {
		t.Fatalf("good feed should still subscribe: %q", res.Summary)
	}
	if !strings.Contains(strings.Join(res.Changes, "\n"), "bad.example.com") {
		t.Fatalf("bad feed failure not surfaced: %v", res.Changes)
	}
}

func TestSubscriptionsDryRunWritesNothing(t *testing.T) {
	f := newURLFetcher()
	f.docs[testFeedURL] = &ingestion.Document{URL: testFeedURL,
		Body: []byte(rssXML("https://example.com/a1"))}
	rc := subsRC(t, f, testFeedURL)
	rc.DryRun = true
	res, err := (Subscriptions{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Changes, "\n"), "would subscribe") {
		t.Fatalf("changes = %v", res.Changes)
	}
	// No state persisted: a real run afterwards still subscribes-from-now.
	rc.DryRun = false
	res2, _ := (Subscriptions{}).Run(context.Background(), rc)
	if !strings.Contains(res2.Summary, "subscribed") {
		t.Fatalf("dry-run leaked state: %q", res2.Summary)
	}
}

func TestSubscriptionsDetectChange(t *testing.T) {
	rc, _ := newRC(t, nil)
	ch, err := (Subscriptions{}).DetectChange(context.Background(), rc)
	if err != nil || ch.Changed {
		t.Fatalf("no feeds: %+v err=%v, want skip", ch, err)
	}
	rc.Config.Subscriptions = config.SubscriptionsConfig{Feeds: []config.Feed{{URL: "https://x.example.com/f"}}}
	ch, _ = (Subscriptions{}).DetectChange(context.Background(), rc)
	if !ch.Changed {
		t.Fatal("feeds configured: want changed")
	}
}

func TestSubscriptionsRegistered(t *testing.T) {
	p := config.Profile{Automations: map[string]config.Automation{
		"subscriptions": {Enabled: true, Schedule: "0 * * * *"},
	}}
	if _, err := Get(p, "subscriptions"); err != nil {
		t.Fatalf("not registered: %v", err)
	}
	if Purpose("subscriptions") == "(no description)" {
		t.Fatal("no catalog purpose")
	}
	if s := Schedulables(p); len(s) != 1 || s[0].Automation.Name() != "subscriptions" {
		t.Fatalf("schedulables = %+v", s)
	}
}
