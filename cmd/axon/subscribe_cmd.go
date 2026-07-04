package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"
	"github.com/mmcdole/gofeed"
	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
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
	cmd.AddCommand(newSubscribeListCmd(gf))
	return cmd
}

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

	// 4. Verification (default on): fetch through the egress-policied
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

	// 5. Append through the comment-preserving editor.
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

// appendAllowDomain appends host to the profile's ingest_domains_allow.
// The edit is surgical — only the allowlist value node is rewritten, in
// flow style (`[...]`), which is valid whether the surrounding policy map
// is flow- or block-styled. (Replacing a whole non-empty flow mapping with
// a block mapping panics inside goccy's renderer, so the whole-block
// rebuild used for subscriptions is not safe here.)
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
	list := append(cfg.Profiles[name].Policy.IngestDomainsAllow, host)

	file, err := parser.ParseBytes(raw, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	rendered, err := yaml.MarshalWithOptions(list, yaml.Flow(true))
	if err != nil {
		return err
	}
	keyPath, err := yaml.PathString(jsonPathFor(name, "policy.ingest_domains_allow"))
	if err != nil {
		return err
	}
	if _, rerr := keyPath.ReadNode(bytes.NewReader(raw)); rerr == nil {
		// Common case: the key exists (starter/example configs write it).
		if err := keyPath.ReplaceWithReader(file, bytes.NewReader(rendered)); err != nil {
			return fmt.Errorf("set ingest_domains_allow: %w", err)
		}
	} else if polPath, perr := yaml.PathString(jsonPathFor(name, "policy")); perr != nil {
		return perr
	} else if _, rerr := polPath.ReadNode(bytes.NewReader(raw)); rerr == nil {
		// Policy exists without the key: merge a flow-styled entry into it.
		entry, merr := yaml.MarshalWithOptions(map[string]any{"ingest_domains_allow": list}, yaml.Flow(true))
		if merr != nil {
			return merr
		}
		if err := polPath.MergeFromReader(file, bytes.NewReader(entry)); err != nil {
			return fmt.Errorf("add ingest_domains_allow: %w", err)
		}
	} else {
		// No policy block at all: merge one into the profile.
		block, merr := yaml.Marshal(map[string]any{"policy": map[string]any{"ingest_domains_allow": list}})
		if merr != nil {
			return merr
		}
		parent, perr2 := yaml.PathString("$.profiles." + name)
		if perr2 != nil {
			return perr2
		}
		if err := parent.MergeFromReader(file, bytes.NewReader(block)); err != nil {
			return fmt.Errorf("add policy: %w", err)
		}
	}

	updated := []byte(file.String())
	if _, err := config.Parse(updated); err != nil {
		return fmt.Errorf("refusing to write: the change makes the config invalid: %w", err)
	}
	return writeFileAtomic(gf.configPath, updated)
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
