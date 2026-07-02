package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/ingestion"
	"github.com/jandro-es/axon/internal/tokens"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

func newIngestCmd(gf *globalFlags) *cobra.Command {
	var dryRun, noApplyLinks, asJSON, enrich bool
	var headers []string
	cmd := &cobra.Command{
		Use:   "ingest <url|path>",
		Short: "Ingest a URL or local text/Markdown file into the knowledge base",
		Long: "Fetch (policy-gated), extract, clean, redact, hash, summarise, write a linked\n" +
			"note in 03-Resources/Knowledge, chunk, embed via Ollama and index for hybrid\n" +
			"search. Idempotent on content hash. Summarisation is deterministic (no Claude\n" +
			"call, zero tokens) unless --enrich routes it through Claude via the token manager.\n\n" +
			"Sources behind SSO (Confluence, internal wikis): configure per-domain credentials\n" +
			"under ingestion.auth in config.yaml, or pass a one-off --header 'Authorization:\n" +
			"Bearer <token>' (applied only to the URL's own domain). Confluence page URLs are\n" +
			"fetched through the Confluence REST API automatically when authenticated.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()

			authRules, err := ingestAuthRules(deps.profile.Ingestion.Auth, headers, args[0])
			if err != nil {
				return err
			}

			// Enrichment is deterministic (zero tokens) by default; --enrich routes
			// it through the token-manager chokepoint so it is budgeted and ledgered.
			var enricher ingestion.Enricher = ingestion.Heuristic{}
			var mgr tokens.Manager
			if enrich {
				mgr = deps.buildServices(nil).manager
				enricher = ingestion.ClaudeEnricher{Manager: mgr, ModelKey: "routine"}
			}

			pipeline := &ingestion.Pipeline{
				Vault:    deps.vault,
				DB:       deps.db,
				Embedder: deps.embedder,
				Enricher: enricher,
				Fetcher:  ingestion.NewHTTPFetcher(deps.profile.Policy, authRules...),
				Policy:   deps.profile.Policy,
				Profile:  deps.name,
			}
			_ = noApplyLinks
			opts := ingestion.IngestOptions{
				DryRun:     dryRun,
				ApplyLinks: false,
				// The CLI is user-initiated, so local-file ingestion is allowed
				// here (it is not on the agent-driven MCP path).
				AllowLocalFiles: true,
			}

			out := cmd.OutOrStdout()

			// Live spinner on a TTY (fetch+embed can take a while); plain path
			// below stays canonical.
			if !asJSON && tui.Interactive(out) {
				var res ingestion.IngestResult
				if err := tui.Spin(out, "ingesting "+args[0]+"…", func() (string, error) {
					var ierr error
					res, ierr = pipeline.Ingest(cmd.Context(), args[0], opts)
					if ierr != nil {
						return "", ierr
					}
					return "ingested " + args[0], nil
				}); err != nil {
					return err
				}
				printIngestResult(out, res)
				printIngestBudget(cmd.Context(), out, mgr, deps.name)
				return nil
			}

			res, runErr := pipeline.Ingest(cmd.Context(), args[0], opts)
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(res); err != nil {
					return err
				}
				return runErr
			}
			if runErr != nil {
				return runErr
			}
			printIngestResult(out, res)
			printIngestBudget(cmd.Context(), out, mgr, deps.name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "do everything except write/embed; print the intended note")
	cmd.Flags().BoolVar(&noApplyLinks, "no-apply-links", false, "do not apply suggested links (default: queued for review)")
	cmd.Flags().BoolVar(&enrich, "enrich", false, "enrich metadata with Claude (via the token manager) instead of deterministically")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the result as JSON")
	cmd.Flags().StringArrayVar(&headers, "header", nil,
		"one-off auth header 'Name: value' scoped to the URL's own domain (repeatable); persistent credentials belong in ingestion.auth")
	return cmd
}

// ingestAuthRules merges the profile's configured ingestion.auth entries with
// one-off --header flags. Flag headers are scoped to the target URL's own host
// so an ad-hoc credential can never follow a redirect to another site.
func ingestAuthRules(configured []config.IngestAuth, headers []string, target string) ([]config.IngestAuth, error) {
	rules := append([]config.IngestAuth(nil), configured...)
	if len(headers) == 0 {
		return rules, nil
	}
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("--header requires a URL target (got %q)", target)
	}
	for _, h := range headers {
		name, value, ok := strings.Cut(h, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || name == "" || value == "" {
			return nil, fmt.Errorf("--header %q must be 'Name: value'", h)
		}
		rules = append(rules, config.IngestAuth{Domain: u.Hostname(), Header: name, Value: value})
	}
	return rules, nil
}

func printIngestResult(out io.Writer, res ingestion.IngestResult) {
	st := ui.For(out)
	switch res.Status {
	case "skipped":
		fmt.Fprintf(out, "%s %s\n", st.Cyan(ui.IconAlready),
			st.Dim(fmt.Sprintf("skipped: %s (%s)", res.Input, res.SkippedReason)))
	case "dry-run":
		fmt.Fprintf(out, "%s would write %s %s — %d chunks\n",
			st.Bold("dry-run:"), st.Cyan(res.NotePath), st.Dim("("+res.Title+")"), res.Chunks)
		fmt.Fprintf(out, "  %s %s\n", st.Dim("enrich:"), enrichLine(st, res))
	default:
		fmt.Fprintf(out, "%s %s\n", st.Green(ui.IconOK), st.Bold(res.Title))
		fmt.Fprintf(out, "  %s %s\n", st.Dim("note:   "), st.Cyan(res.NotePath))
		fmt.Fprintf(out, "  %s %d (%d embedded)\n", st.Dim("chunks: "), res.Chunks, res.Embedded)
		fmt.Fprintf(out, "  %s %s\n", st.Dim("enrich: "), enrichLine(st, res))
		if res.Redacted {
			fmt.Fprintf(out, "  %s %s\n", st.Dim("redact: "), st.Yellow("applied"))
		}
		if len(res.Suggestions) > 0 {
			fmt.Fprintf(out, "  %s %d suggestion(s) queued in .axon/review-queue.md\n",
				st.Dim("links:  "), len(res.Suggestions))
		}
	}
}

// enrichLine renders how the metadata was produced and its token cost.
func enrichLine(st ui.Styler, res ingestion.IngestResult) string {
	kind := res.EnrichKind
	if kind == "" {
		kind = "heuristic"
	}
	if kind != "claude" {
		return st.Dim("heuristic · 0 tokens")
	}
	model := res.EnrichModel
	if model == "" {
		model = "claude"
	}
	return fmt.Sprintf("%s · %s tokens %s",
		st.Cyan(model),
		st.Bold(humanize.Comma(int64(res.Tokens))),
		st.Dim(fmt.Sprintf("(in %d / out %d)", res.InputTokens, res.OutputTokens)))
}

// printIngestBudget shows remaining budget after a token-spending ingest.
func printIngestBudget(ctx context.Context, out io.Writer, mgr tokens.Manager, profile string) {
	if mgr == nil {
		return
	}
	stStatus, err := mgr.Status(ctx, profile)
	if err != nil {
		return
	}
	st := ui.For(out)
	line := fmt.Sprintf("day %.1f%% used, week %.1f%% used", stStatus.Day.Pct, stStatus.Week.Pct)
	if stStatus.GuardPaused {
		line += " — " + stStatus.GuardReason
		fmt.Fprintf(out, "  %s %s\n", st.Dim("budget: "), st.Yellow(line))
		return
	}
	fmt.Fprintf(out, "  %s %s\n", st.Dim("budget: "), st.Dim(line))
}
