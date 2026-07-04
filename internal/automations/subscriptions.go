package automations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/ingestion"
)

const (
	// subscriptionsSeenState is the automation_state key for the per-feed
	// seen-item memory (ADR-019).
	subscriptionsSeenState = "subscriptions:seen"
	// subsSeenCapPerFeed bounds each feed's seen list (keep the newest).
	subsSeenCapPerFeed = 500
	// subscriptionsHTTPState stores per-feed HTTP cache validators for
	// conditional polling (FR-101): map[feedURL]ingestion.Validators.
	subscriptionsHTTPState = "subscriptions:http"
)

// Subscriptions polls the config-declared RSS/Atom feeds and ingests new
// items through the standard pipeline (ADR-019, FR-91…FR-93). Feed XML is
// fetched by the same egress-policied fetcher as every ingest; gofeed
// tolerates the wild-feed chaos. Subscribe-from-now, per-tick caps and
// mark-seen-after-attempt keep the volume bounded.
type Subscriptions struct{}

func (Subscriptions) Name() string    { return "subscriptions" }
func (Subscriptions) Essential() bool { return false }

func (Subscriptions) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	n := len(rc.Config.Subscriptions.Feeds)
	if n == 0 {
		return Change{Changed: false, Reason: "no feeds configured"}, nil
	}
	// Feeds change remotely; the poll IS the check. The schedule is the cadence.
	return Change{Changed: true, Reason: fmt.Sprintf("%d feed(s) to poll", n)}, nil
}

func (Subscriptions) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	cfg := rc.Config.Subscriptions
	seen := loadSubsSeen(ctx, rc)
	pl := enrichedPipeline(rc, cfg.EnrichMode())
	parser := gofeed.NewParser()

	cond, _ := rc.Pipeline.Fetcher.(ingestion.ConditionalFetcher)
	validators := loadSubsHTTP(ctx, rc)

	var (
		changes                                           []string
		ingested, failed, subscribed, feedErrs, unchanged int
	)

	for _, feed := range cfg.Feeds {
		var doc *ingestion.Document
		var err error
		if cond != nil {
			var notModified bool
			doc, notModified, err = cond.FetchConditional(ctx, feed.URL, validators[feed.URL])
			if err == nil && notModified {
				// The server asserts nothing changed (304): free skip —
				// no body, no parse, seen-state untouched (FR-101).
				unchanged++
				changes = append(changes, "feed unchanged (304): "+feed.URL)
				continue
			}
		} else {
			doc, err = rc.Pipeline.Fetcher.Fetch(ctx, feed.URL)
		}
		if err != nil {
			feedErrs++
			changes = append(changes, fmt.Sprintf("feed FAILED: %s — %v", feed.URL, err))
			continue
		}
		parsed, err := parser.Parse(bytes.NewReader(doc.Body))
		if err != nil {
			feedErrs++
			changes = append(changes, fmt.Sprintf("feed FAILED: %s — parse: %v", feed.URL, err))
			continue
		}
		links := itemLinks(parsed)

		if v := (ingestion.Validators{ETag: doc.ETag, LastModified: doc.LastModified}); v != (ingestion.Validators{}) {
			validators[feed.URL] = v
		} else {
			delete(validators, feed.URL) // feed stopped sending validators
		}

		seenList, known := seen[feed.URL]
		if !known {
			// Subscribe-from-now (FR-92): record, ingest nothing.
			if rc.DryRun {
				changes = append(changes, fmt.Sprintf("would subscribe %s (%d existing entries)", feed.URL, len(links)))
				continue
			}
			seen[feed.URL] = capSeen(links)
			subscribed++
			changes = append(changes, fmt.Sprintf("subscribed %s (%d existing entries marked seen)", feed.URL, len(links)))
			continue
		}

		seenSet := map[string]bool{}
		for _, u := range seenList {
			seenSet[u] = true
		}
		budget := cfg.PerTick()
		for _, link := range links {
			if budget == 0 {
				break
			}
			if seenSet[link] {
				continue
			}
			if src, _ := db.GetSourceByURL(ctx, rc.DB, link); src != nil {
				seenList = append(seenList, link) // already ingested elsewhere: remember
				seenSet[link] = true
				continue
			}
			if rc.DryRun {
				changes = append(changes, "would ingest "+link)
				budget--
				continue
			}
			res, ierr := pl.Ingest(ctx, link, ingestion.IngestOptions{})
			// Mark seen after the attempt, success OR failure (FR-92):
			// one try per item; explicit `axon ingest` is the retry path.
			seenList = append(seenList, link)
			seenSet[link] = true
			budget--
			if ierr != nil {
				failed++
				changes = append(changes, fmt.Sprintf("FAILED: %s — %v", link, ierr))
				continue
			}
			ingested++
			changes = append(changes, fmt.Sprintf("%s → %s", link, res.NotePath))
		}
		seen[feed.URL] = capSeen(seenList)
	}

	if !rc.DryRun {
		saveSubsSeen(ctx, rc, seen)
		saveSubsHTTP(ctx, rc, validators, cfg.Feeds)
	}

	summary := fmt.Sprintf("ingested %d item(s) from %d feed(s), %d failed", ingested, len(cfg.Feeds), failed)
	if subscribed > 0 {
		summary += fmt.Sprintf("; subscribed %d new feed(s)", subscribed)
	}
	if feedErrs > 0 {
		summary += fmt.Sprintf("; %d feed(s) unreachable", feedErrs)
	}
	if unchanged > 0 {
		summary += fmt.Sprintf("; %d unchanged (304)", unchanged)
	}
	if rc.DryRun {
		summary = "dry-run: " + summary + " (would subscribe/ingest as listed)"
	}
	return RunResult{Summary: summary, Changes: changes}, nil
}

// itemLinks extracts non-empty item links, newest first (published/updated
// date when present, feed order otherwise), deduplicated.
func itemLinks(f *gofeed.Feed) []string {
	items := make([]*gofeed.Item, 0, len(f.Items))
	items = append(items, f.Items...)
	sort.SliceStable(items, func(i, j int) bool {
		return itemTime(items[i]).After(itemTime(items[j]))
	})
	var links []string
	seen := map[string]bool{}
	for _, it := range items {
		l := strings.TrimSpace(it.Link)
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		links = append(links, l)
	}
	return links
}

func itemTime(it *gofeed.Item) time.Time {
	if it.PublishedParsed != nil {
		return *it.PublishedParsed
	}
	if it.UpdatedParsed != nil {
		return *it.UpdatedParsed
	}
	return time.Time{}
}

// capSeen keeps the newest subsSeenCapPerFeed entries (append order = age).
func capSeen(list []string) []string {
	if len(list) > subsSeenCapPerFeed {
		return list[len(list)-subsSeenCapPerFeed:]
	}
	return list
}

// loadSubsSeen reads the per-feed seen memory (empty on any problem — worst
// case a feed re-subscribes-from-now).
func loadSubsSeen(ctx context.Context, rc RunCtx) map[string][]string {
	out := map[string][]string{}
	raw, err := db.GetCursor(ctx, rc.DB, subscriptionsSeenState)
	if err != nil || raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// loadSubsHTTP reads the per-feed HTTP validators (empty on any problem —
// worst case one unconditional GET per feed).
func loadSubsHTTP(ctx context.Context, rc RunCtx) map[string]ingestion.Validators {
	out := map[string]ingestion.Validators{}
	raw, err := db.GetCursor(ctx, rc.DB, subscriptionsHTTPState)
	if err != nil || raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// saveSubsHTTP persists the validators, pruned to currently-configured
// feeds so removed subscriptions self-clean.
func saveSubsHTTP(ctx context.Context, rc RunCtx, m map[string]ingestion.Validators, feeds []config.Feed) {
	keep := map[string]ingestion.Validators{}
	for _, f := range feeds {
		if v, ok := m[f.URL]; ok {
			keep[f.URL] = v
		}
	}
	raw, err := json.Marshal(keep)
	if err != nil {
		return
	}
	if err := db.SetCursor(ctx, rc.DB, subscriptionsHTTPState, string(raw), rc.now().UTC().Format(time.RFC3339)); err != nil {
		rc.Log.Warn("subscriptions: persist http validators", "err", err)
	}
}

// saveSubsSeen persists the seen memory beside the engine cursors.
func saveSubsSeen(ctx context.Context, rc RunCtx, seen map[string][]string) {
	raw, err := json.Marshal(seen)
	if err != nil {
		return
	}
	if err := db.SetCursor(ctx, rc.DB, subscriptionsSeenState, string(raw), rc.now().UTC().Format(time.RFC3339)); err != nil {
		rc.Log.Warn("subscriptions: persist seen-state", "err", err)
	}
}
