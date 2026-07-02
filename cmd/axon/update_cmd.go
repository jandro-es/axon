package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/selfupdate"
	"github.com/jandro-es/axon/internal/tui"
	"github.com/jandro-es/axon/internal/ui"
)

// updateRepoOwner/Name are AXON's release coordinates (module path).
const (
	updateRepoOwner = "jandro-es"
	updateRepoName  = "axon"
)

// updateBaseURL honours AXON_UPDATE_BASE_URL (tests, mirrors).
func updateBaseURL() string { return os.Getenv("AXON_UPDATE_BASE_URL") }

// updateCache is the daily availability check persisted at
// <AXON_HOME>/update-check.json — read by doctor and the dashboard health
// payload so they never block on the network.
type updateCache struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

func updateCachePath() string { return filepath.Join(config.AxonHome(), "update-check.json") }

func readUpdateCache() (updateCache, bool) {
	var c updateCache
	raw, err := os.ReadFile(updateCachePath())
	if err != nil || json.Unmarshal(raw, &c) != nil || c.Latest == "" {
		return updateCache{}, false
	}
	return c, true
}

func writeUpdateCache(latest string) {
	raw, err := json.Marshal(updateCache{Latest: latest, CheckedAt: time.Now().UTC()})
	if err != nil {
		return
	}
	_ = os.MkdirAll(config.AxonHome(), 0o755)
	_ = os.WriteFile(updateCachePath(), raw, 0o644)
}

// refreshUpdateCache checks the latest release and records it. Errors are
// swallowed — availability is best-effort telemetry, never a failure.
func refreshUpdateCache(ctx context.Context) {
	rel, err := selfupdate.CheckLatest(ctx, updateBaseURL(), updateRepoOwner, updateRepoName)
	if err != nil {
		return
	}
	writeUpdateCache(rel.Version)
}

func newUpdateCmd(gf *globalFlags) *cobra.Command {
	var checkOnly, asJSON bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update axon to the latest release (verified download, atomic swap)",
		Long: "Check GitHub Releases for a newer version; download the binary for this\n" +
			"platform, verify its SHA-256 against the release's checksums.txt, and swap\n" +
			"it in atomically (the previous binary survives as axon.old until success).\n" +
			"Restart of the daemon/service and `axon init` convergence are announced as\n" +
			"next steps. Never automatic — updating is always this explicit command.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			st := ui.For(out)
			current, _, _ := buildVersion()

			rel, err := selfupdate.CheckLatest(cmd.Context(), updateBaseURL(), updateRepoOwner, updateRepoName)
			if err != nil {
				return fmt.Errorf("update check: %w", err)
			}
			writeUpdateCache(rel.Version)
			newer := selfupdate.IsNewer(current, rel.Version)

			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{
					"current": current, "latest": rel.Version, "update_available": newer, "check_only": checkOnly,
				})
			}
			if !newer {
				fmt.Fprintf(out, "%s axon %s is up to date (latest release: %s)\n", st.Green(ui.IconOK), st.Bold(current), rel.Version)
				return nil
			}
			fmt.Fprintf(out, "%s update available: %s → %s\n", st.Cyan(ui.IconRocket), current, st.Bold(rel.Version))
			if checkOnly {
				fmt.Fprintf(out, "%s %s\n", st.Yellow(ui.IconArrow), st.Dim("run `axon update` to install it"))
				return nil
			}

			target, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locate running binary: %w", err)
			}
			target, _ = filepath.EvalSymlinks(target)

			name := selfupdate.AssetName(rel.Version, runtime.GOOS, runtime.GOARCH)
			var downloaded string
			if err := tui.Spin(out, fmt.Sprintf("downloading %s (verified)…", name), func() (string, error) {
				dir, derr := os.MkdirTemp("", "axon-update")
				if derr != nil {
					return "", derr
				}
				downloaded, derr = selfupdate.DownloadVerified(cmd.Context(), rel, name, dir)
				if derr != nil {
					return "", derr
				}
				return "downloaded + checksum verified", nil
			}); err != nil {
				return err
			}

			if err := selfupdate.Swap(target, downloaded); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s installed %s at %s %s\n", st.Green(ui.IconOK), st.Bold(rel.Version), target,
				st.Dim("(previous kept as axon.old)"))
			fmt.Fprintf(out, "%s next: restart the daemon (`axon stop && axon start`, or `make reload` for a service install) and run `axon init` to converge\n",
				st.Yellow(ui.IconArrow))
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check-only", false, "only report whether an update is available")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the check result as JSON")
	return cmd
}
