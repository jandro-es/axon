package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/dashboard"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/scheduler"
	"github.com/jandro-es/axon/internal/selfupdate"
	"github.com/jandro-es/axon/internal/ui"
	"github.com/jandro-es/axon/web"
)

func newStartCmd(gf *globalFlags) *cobra.Command {
	var once, noDashboard bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the daemon: scheduler + live dashboard",
		Long: "Run the in-daemon scheduler (every enabled, policy-permitted automation on\n" +
			"its cron schedule, through the engine) and serve the live dashboard at\n" +
			"dashboard.host:port. Runs until interrupted.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Stamped before any work so /health's uptime measures the whole
			// process lifetime, not the time since the dashboard bound.
			daemonStartedAt := time.Now()

			deps, err := loadProfileDeps(gf, true)
			if err != nil {
				return err
			}

			bus := events.NewBus()
			logger := events.NewLogger(cmd.OutOrStdout(), events.FormatText, "info")
			svc := deps.buildServices(bus)
			out := cmd.OutOrStdout()
			st := ui.For(out)
			fmt.Fprintln(out, st.Header(ui.IconRocket, fmt.Sprintf("axon start — profile %q", deps.name)))

			// Refuse to run as root over a user-owned vault: root-created notes
			// come out 0600 root-owned and lock the real daemon out of them.
			if err := checkNotRoot(deps.paths.DataDir, deps.paths.VaultPath); err != nil {
				return err
			}

			// Refuse to double-start: a second daemon on the same profile would
			// double-run every automation (the engine's locks are in-process).
			if err := checkNotRunning(deps.paths.DataDir); err != nil {
				return err
			}

			// Record the pid so `axon stop` can signal this daemon (FR-04).
			if pidPath, perr := writePidFile(deps.paths.DataDir); perr != nil {
				fmt.Fprintf(out, "%s could not write pidfile: %v\n", st.Yellow(ui.IconWarn), perr)
			} else {
				defer removePidFile(deps.paths.DataDir)
				fmt.Fprintf(out, "%s %s\n", st.Dim("pid"), st.Dim(fmt.Sprintf("%d (%s)", os.Getpid(), pidPath)))
			}

			sigCtx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			ctx, cancel := context.WithCancel(sigCtx)

			// Shutdown order matters: cancel the context (so the dashboard +
			// PersistEvents goroutines return), WAIT for them, and only THEN close
			// the bus and DB — otherwise those goroutines race a closed DB/bus.
			var wg sync.WaitGroup
			defer func() {
				cancel()
				wg.Wait()
				bus.Close()
				deps.close()
			}()

			// Persist every event to the events table for the activity-feed history.
			wg.Add(1)
			go func() {
				defer wg.Done()
				dashboard.PersistEvents(ctx, bus, deps.db)
			}()

			// Schedule automations. A recipe colliding with a built-in name
			// refuses startup loudly (FR-201) rather than silently shadowing.
			if err := automations.ValidateRecipes(deps.profile); err != nil {
				return err
			}
			sched := scheduler.New(scheduler.Options{Log: logger, Jitter: 5 * time.Second})
			for _, s := range automations.Schedulables(deps.profile) {
				a := s.Automation
				job := scheduler.Job{
					Name: a.Name(), Schedule: s.Schedule, CatchUp: s.CatchUp,
					Run: func(ctx context.Context) error {
						_, runErr := svc.engine.Run(ctx, a, false)
						return runErr
					},
				}
				if err := sched.Add(job); err != nil {
					fmt.Fprintf(out, "%s skip %s: %v\n", st.Yellow(ui.IconWarn), a.Name(), err)
					continue
				}
				fmt.Fprintf(out, "%s scheduled %-18s %s\n", st.Green(ui.IconOK), a.Name(), st.Dim(s.Schedule))
			}

			// Serve the dashboard.
			var dash *dashboard.Server
			if !noDashboard {
				dash = dashboard.New(dashboard.Config{
					Profile:        deps.name,
					Host:           deps.profile.Dashboard.Host,
					Port:           deps.profile.Dashboard.Port,
					DB:             deps.db,
					Manager:        svc.manager,
					Bus:            bus,
					Static:         web.Assets(),
					Vault:          deps.vault,
					Searcher:       svc.searcher,
					Retrieval:      deps.profile.Retrieval,
					AskEnabled:     deps.profile.Dashboard.AskAllowed(),
					CaptureEnabled: deps.profile.Dashboard.CaptureAllowed(),
					RelatedEnabled: deps.profile.Dashboard.RelatedAllowed(),
					ActionsEnabled: deps.profile.Dashboard.ActionsAllowed(),
					SearchEnabled:  deps.profile.Dashboard.SearchAllowed(),
					Health: func(context.Context) map[string]any {
						h := map[string]any{
							"embeddings_provider": deps.profile.Embeddings.Provider,
							"embeddings_model":    deps.profile.Embeddings.Model,
							"embeddings_dim":      deps.profile.Embeddings.Dim,
							// When this daemon process began serving. Clients
							// derive uptime from it rather than tracking their
							// own "first seen" time, which resets on client
							// restart and lies across a daemon restart.
							"started_at": daemonStartedAt.UTC().Format(time.RFC3339),
							// The claude binary THIS process resolves on its
							// own PATH, or "" when it resolves none. Only the
							// daemon can answer that: a service unit corrected
							// on disk does not reach a launchd/systemd job that
							// is still running the definition it was loaded
							// with, so doctor reading the unit file cannot tell
							// a healthy daemon from one that will fail every
							// Claude-backed automation. Path, never PATH — one
							// resolved location, not the environment around it.
							"claude_path": lookPathOrEmpty("claude"),
						}
						current, _, _ := buildVersion()
						h["version"] = current
						if c, ok := readUpdateCache(); ok {
							h["latest_version"] = c.Latest
							h["update_available"] = selfupdate.IsNewer(current, c.Latest)
						}
						return h
					},
				})
				// Bind before the scheduler starts, and treat a failure as fatal.
				// The usual cause is another daemon already holding the port —
				// exactly the case the pidfile guard exists to prevent — and
				// carrying on would leave a second, invisible scheduler writing
				// the vault with no dashboard to notice it by.
				ln, lerr := dash.Listen()
				if lerr != nil {
					return fmt.Errorf("dashboard: %w (another daemon may already be running; "+
						"use --no-dashboard to run headless)", lerr)
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := dash.Serve(ctx, ln); err != nil {
						fmt.Fprintf(out, "%s dashboard: %v\n", st.Yellow(ui.IconWarn), err)
					}
				}()
				fmt.Fprintf(out, "%s dashboard: %s\n", st.Cyan(ui.IconChart), st.Cyan("http://"+dash.Addr()))
			}

			sched.Start(ctx)
			sched.CatchUp(ctx)

			// Daily release-availability check (cache only — doctor and the
			// dashboard read it; updating is always the user's explicit action).
			wg.Add(1)
			go func() {
				defer wg.Done()
				refreshUpdateCache(ctx)
				tick := time.NewTicker(24 * time.Hour)
				defer tick.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-tick.C:
						refreshUpdateCache(ctx)
					}
				}
			}()

			fmt.Fprintln(out, st.Green(ui.IconOK+" daemon running")+st.Dim(" — press Ctrl-C to stop"))

			if once {
				// Test/inspection mode: don't block on signals. The deferred
				// cancel()+wg.Wait() tears down the dashboard/persister cleanly.
				<-sched.Stop().Done()
				return nil
			}
			<-ctx.Done()
			fmt.Fprintln(out, st.Dim("\nstopping…"))
			<-sched.Stop().Done()
			return nil
		},
	}
	cmd.Flags().BoolVar(&once, "once", false, "register schedules, run catch-up, then exit (no blocking)")
	cmd.Flags().BoolVar(&noDashboard, "no-dashboard", false, "run the scheduler without serving the dashboard")
	return cmd
}

// lookPathOrEmpty resolves name on this process's PATH, reporting "" rather
// than an error when it is not there. Absence is the answer here, not a
// failure: /health states what the daemon can reach, and "nothing" is a
// perfectly reportable state.
func lookPathOrEmpty(name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}
