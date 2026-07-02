package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/dashboard"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/scheduler"
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

			// Schedule automations.
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
					Profile: deps.name,
					Host:    deps.profile.Dashboard.Host,
					Port:    deps.profile.Dashboard.Port,
					DB:      deps.db,
					Manager: svc.manager,
					Bus:     bus,
					Static:  web.Assets(),
					Health: func(context.Context) map[string]any {
						return map[string]any{
							"embeddings_provider": deps.profile.Embeddings.Provider,
							"embeddings_model":    deps.profile.Embeddings.Model,
							"embeddings_dim":      deps.profile.Embeddings.Dim,
						}
					},
				})
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := dash.ListenAndServe(ctx); err != nil {
						fmt.Fprintf(out, "%s dashboard: %v\n", st.Yellow(ui.IconWarn), err)
					}
				}()
				fmt.Fprintf(out, "%s dashboard: %s\n", st.Cyan(ui.IconChart), st.Cyan("http://"+dash.Addr()))
			}

			sched.Start(ctx)
			sched.CatchUp(ctx)
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
