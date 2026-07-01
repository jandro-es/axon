package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/search"
	"github.com/jandro-es/axon/internal/ui"
)

func newSearchCmd(gf *globalFlags) *cobra.Command {
	var topK int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Hybrid (lexical + semantic) search across the vault + knowledge",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}
			defer deps.close()

			query := strings.Join(args, " ")
			s := search.New(deps.db, deps.embedder)
			hits, err := s.Search(cmd.Context(), query, topK)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(hits)
			}
			sty := ui.For(out)
			if len(hits) == 0 {
				fmt.Fprintf(out, "%s %s\n", sty.Yellow(ui.IconSearch), sty.Dim(fmt.Sprintf("no results for %q", query)))
				return nil
			}
			fmt.Fprintf(out, "%s %s\n", ui.IconSearch, sty.Dim(fmt.Sprintf("%d result(s) for %q", len(hits), query)))
			for i, h := range hits {
				fmt.Fprintf(out, "%s %s  %s\n",
					sty.Dim(fmt.Sprintf("%d.", i+1)),
					sty.Bold(sty.Cyan(h.Path)),
					sty.Dim(fmt.Sprintf("(score %.4f  lex %.2f  vec %.3f)", h.Score, h.Lexical, h.Vector)))
				fmt.Fprintf(out, "   %s\n", h.Snippet)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 8, "number of results to return")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit results as JSON")
	return cmd
}
