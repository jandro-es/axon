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
