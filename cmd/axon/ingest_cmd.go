package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/ingestion"
)

func newIngestCmd(gf *globalFlags) *cobra.Command {
	var dryRun, noApplyLinks, asJSON bool
	cmd := &cobra.Command{
		Use:   "ingest <url|path>",
		Short: "Ingest a URL or local text/Markdown file into the knowledge base",
		Long: "Fetch (policy-gated), extract, clean, redact, hash, summarise (deterministically\n" +
			"in Phase 2 — no Claude call), write a linked note in 03-Resources/Knowledge,\n" +
			"chunk, embed via Ollama and index for hybrid search. Idempotent on content hash.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()

			pipeline := &ingestion.Pipeline{
				Vault:    deps.vault,
				DB:       deps.db,
				Embedder: deps.embedder,
				Enricher: ingestion.Heuristic{},
				Fetcher:  ingestion.NewHTTPFetcher(),
				Policy:   deps.profile.Policy,
				Profile:  deps.name,
			}
			// Phase 2 always queues link suggestions for human review (auto-apply
			// is a later, opt-in feature); --no-apply-links is the explicit default.
			_ = noApplyLinks
			res, runErr := pipeline.Ingest(cmd.Context(), args[0], ingestion.IngestOptions{
				DryRun:     dryRun,
				ApplyLinks: false,
			})

			out := cmd.OutOrStdout()
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
			printIngestResult(cmd, res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "do everything except write/embed; print the intended note")
	cmd.Flags().BoolVar(&noApplyLinks, "no-apply-links", false, "do not apply suggested links (default: queued for review)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the result as JSON")
	return cmd
}

func printIngestResult(cmd *cobra.Command, res ingestion.IngestResult) {
	out := cmd.OutOrStdout()
	switch res.Status {
	case "skipped":
		fmt.Fprintf(out, "↻ skipped: %s (%s)\n", res.Input, res.SkippedReason)
	case "dry-run":
		fmt.Fprintf(out, "dry-run: would write %q (%q) — %d chunks\n", res.NotePath, res.Title, res.Chunks)
	default:
		fmt.Fprintf(out, "✓ %s: %s\n", res.Status, res.Title)
		fmt.Fprintf(out, "  note:    %s\n", res.NotePath)
		fmt.Fprintf(out, "  chunks:  %d (%d embedded)\n", res.Chunks, res.Embedded)
		if res.Redacted {
			fmt.Fprintln(out, "  redaction: applied")
		}
		if len(res.Suggestions) > 0 {
			fmt.Fprintf(out, "  links:   %d suggestion(s) queued in .axon/review-queue.md\n", len(res.Suggestions))
		}
	}
}
