package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/dashboard"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/scheduler"
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
			defer deps.close()

			bus := events.NewBus()
			defer bus.Close()
			logger := events.NewLogger(cmd.OutOrStdout(), events.FormatText, "info")
			svc := deps.buildServices(bus)
			out := cmd.OutOrStdout()

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Persist every event to the events table for the activity-feed history.
			go dashboard.PersistEvents(ctx, bus, deps.db)

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
					fmt.Fprintf(out, "⚠ skip %s: %v\n", a.Name(), err)
					continue
				}
				fmt.Fprintf(out, "scheduled %-18s %s\n", a.Name(), s.Schedule)
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
				})
				go func() {
					if err := dash.ListenAndServe(ctx); err != nil {
						fmt.Fprintf(out, "⚠ dashboard: %v\n", err)
					}
				}()
				fmt.Fprintf(out, "dashboard: http://%s\n", dash.Addr())
			}

			sched.Start(ctx)
			sched.CatchUp(ctx)
			fmt.Fprintln(out, "daemon running — press Ctrl-C to stop")

			if once {
				// Test/inspection mode: don't block on signals.
				<-sched.Stop().Done()
				return nil
			}
			<-ctx.Done()
			fmt.Fprintln(out, "\nstopping…")
			<-sched.Stop().Done()
			return nil
		},
	}
	cmd.Flags().BoolVar(&once, "once", false, "register schedules, run catch-up, then exit (no blocking)")
	cmd.Flags().BoolVar(&noDashboard, "no-dashboard", false, "run the scheduler without serving the dashboard")
	return cmd
}
