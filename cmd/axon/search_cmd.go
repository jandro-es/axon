package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/search"
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
			if len(hits) == 0 {
				fmt.Fprintf(out, "no results for %q\n", query)
				return nil
			}
			for i, h := range hits {
				fmt.Fprintf(out, "%d. %s  (score %.4f  lex %.2f  vec %.3f)\n", i+1, h.Path, h.Score, h.Lexical, h.Vector)
				fmt.Fprintf(out, "   %s\n", h.Snippet)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&topK, "top-k", 8, "number of results to return")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit results as JSON")
	return cmd
}
