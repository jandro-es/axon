package main

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/core"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

func newConfigureEmbeddingsCmd(gf *globalFlags) *cobra.Command {
	var model string
	var dim int
	var doReindex bool
	cmd := &cobra.Command{
		Use:   "embeddings <ollama|apple>",
		Short: "Switch the embeddings provider (persist + converge + re-embed, one flow)",
		Long: "Switching to apple sets the on-device defaults automatically; switching to\n" +
			"ollama needs --model and --dim (there is no way to guess your Ollama model).\n" +
			"--reindex runs the mandatory re-embed immediately; otherwise the pending\n" +
			"`axon reindex --embeddings` is announced loudly.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := args[0]
			if provider != "ollama" && provider != "apple" {
				return fmt.Errorf("provider must be ollama or apple (got %q)", provider)
			}
			return switchEmbeddings(cmd, gf, provider, model, dim, doReindex)
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "embedding model (required for ollama; apple defaults to "+config.AppleEmbeddingModel+")")
	cmd.Flags().IntVar(&dim, "dim", 0, "vector dimension (required for ollama; apple defaults to "+strconv.Itoa(config.AppleEmbeddingDim)+")")
	cmd.Flags().BoolVar(&doReindex, "reindex", false, "re-embed the index immediately after switching")
	return cmd
}

// switchEmbeddings is the one-flow provider switch used by both the configure
// menu and the subcommand: persist provider+model+dim → converge (compile the
// Apple helper / verify the Ollama model) → re-embed (confirmed interactively,
// or opted in with --reindex, otherwise announced as pending).
func switchEmbeddings(cmd *cobra.Command, gf *globalFlags, provider, model string, dim int, doReindex bool) error {
	out := cmd.OutOrStdout()
	st := ui.For(out)

	if provider == "apple" {
		if model == "" {
			model = config.AppleEmbeddingModel
		}
		if dim == 0 {
			dim = config.AppleEmbeddingDim
		}
	}
	if provider == "ollama" && (model == "" || dim == 0) {
		if !tui.Interactive(out) {
			return fmt.Errorf("switching to ollama needs --model and --dim (e.g. --model nomic-embed-text --dim 768)")
		}
		var err error
		model, err = tui.Input(out, cmd.InOrStdin(), "Ollama embedding model", "nomic-embed-text", "nomic-embed-text")
		if err != nil {
			return err
		}
		d, err := tui.Input(out, cmd.InOrStdin(), "Vector dimension of that model", "768", "768")
		if err != nil {
			return err
		}
		dim, err = strconv.Atoi(d)
		if err != nil {
			return fmt.Errorf("dimension must be a number: %w", err)
		}
	}

	for _, kv := range [][2]string{
		{"embeddings.provider", provider},
		{"embeddings.model", model},
		{"embeddings.dim", strconv.Itoa(dim)},
	} {
		if err := setConfigValue(gf.configPath, gf.profile, kv[0], kv[1]); err != nil {
			return fmt.Errorf("persist %s: %w", kv[0], err)
		}
	}
	fmt.Fprintf(out, "%s embeddings: provider=%s model=%s dim=%d\n", st.Green(ui.IconOK), provider, model, dim)

	// Converge with the NEW config (compile helper / check model+dim).
	cfg, err := config.Load(gf.configPath)
	if err != nil {
		return err
	}
	_, profile, err := cfg.ResolveProfile(gf.profile)
	if err != nil {
		return err
	}
	if err := tui.Spin(out, "converging "+provider+" embeddings…", func() (string, error) {
		res := core.ProbeEmbeddings(cmd.Context(), profile.Embeddings)
		if res.Status == core.StepFailed {
			return "", fmt.Errorf("%s", res.Detail)
		}
		return res.Detail, nil
	}); err != nil {
		return err
	}

	if !doReindex && tui.Interactive(out) {
		doReindex = tui.Confirm(out, cmd.InOrStdin(),
			"Switching providers changes vector dimensions — re-embed the index now?", true)
	}
	if doReindex {
		deps, err := loadProfileDeps(gf, true)
		if err != nil {
			return err
		}
		defer deps.close()
		return tui.Spin(out, "re-embedding index…", func() (string, error) {
			re, err := core.ReembedPending(cmd.Context(), deps.db, deps.embedder, true)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("re-embedded %d/%d chunks via %s", re.Embedded, re.Total, model), nil
		})
	}
	fmt.Fprintf(out, "%s %s\n", st.Yellow(ui.IconArrow),
		st.Bold("pending: run `axon reindex --embeddings` to re-vectorise the index for the new provider"))
	return nil
}
