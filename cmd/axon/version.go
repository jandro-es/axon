package main

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/selfupdate"
	"github.com/jandro-es/axon/internal/ui"
)

// Build metadata. The Makefile stamps all three via -ldflags at build time
// (`make build`/`make release`). When they are absent — a plain `go build` or
// `go install` — buildVersion() recovers what it can from Go's embedded build
// info (the VCS commit/time it records automatically, or the module version for
// `go install …@vX.Y.Z`), so EVERY build reports a real, checkable version.
var (
	version = ""
	commit  = ""
	date    = ""
)

// buildVersion resolves (version, commit, date), preferring -ldflags values and
// falling back to runtime/debug build info. It never returns empty strings.
func buildVersion() (v, c, d string) {
	v, c, d = version, commit, date

	if info, ok := debug.ReadBuildInfo(); ok {
		var rev, when, dirty string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.time":
				when = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "-dirty"
				}
			}
		}
		shortRev := rev
		if len(shortRev) > 12 {
			shortRev = shortRev[:12]
		}
		if c == "" {
			c = rev
		}
		if d == "" {
			d = when
		}
		if v == "" {
			switch {
			case shortRev != "":
				// Built from a VCS checkout (`go build`/`go install` in the repo):
				// the short commit is clean and matches `git describe`. Go's
				// Main.Version here is only "(devel)" or a noisy pseudo-version.
				v = shortRev + dirty
			case info.Main.Version != "" && info.Main.Version != "(devel)":
				// Installed from the module proxy at a tag (`…@vX.Y.Z`): no VCS
				// stamps are present, but Main.Version carries the real tag.
				v = info.Main.Version
			}
		}
	}

	if v == "" {
		v = "dev"
	}
	if c == "" {
		c = "none"
	}
	if d == "" {
		d = "unknown"
	}
	return v, c, d
}

func newVersionCmd() *cobra.Command {
	var short, check bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the axon version and build metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			st := ui.For(out)
			v, c, d := buildVersion()
			if short {
				fmt.Fprintln(out, v)
				return nil
			}
			fmt.Fprintf(out, "%s %s %s\n", ui.IconRocket, st.Bold("axon"), st.Cyan(v))
			fmt.Fprintf(out, "  %s %s\n", st.Dim("commit:"), c)
			fmt.Fprintf(out, "  %s %s\n", st.Dim("built: "), d)
			fmt.Fprintf(out, "  %s %s %s/%s\n", st.Dim("go:    "), runtime.Version(), runtime.GOOS, runtime.GOARCH)
			if check {
				rel, err := selfupdate.CheckLatest(cmd.Context(), updateBaseURL(), updateRepoOwner, updateRepoName)
				if err != nil {
					return fmt.Errorf("update check: %w", err)
				}
				writeUpdateCache(rel.Version)
				if selfupdate.IsNewer(v, rel.Version) {
					fmt.Fprintf(out, "  %s %s %s\n", st.Dim("latest:"), st.Bold(rel.Version), st.Yellow("(update available — run `axon update`)"))
				} else {
					fmt.Fprintf(out, "  %s %s %s\n", st.Dim("latest:"), rel.Version, st.Green("(up to date)"))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print just the version string (for scripts)")
	cmd.Flags().BoolVar(&check, "check", false, "also check GitHub Releases for a newer version")
	return cmd
}
