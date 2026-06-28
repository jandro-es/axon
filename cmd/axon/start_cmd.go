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
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/scheduler"
)

func newStartCmd(gf *globalFlags) *cobra.Command {
	var once bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the automation daemon (scheduler)",
		Long: "Run the in-daemon scheduler: register every enabled, policy-permitted\n" +
			"automation on its cron schedule and run them through the engine. Runs until\n" +
			"interrupted. (The dashboard server arrives in Phase 6.)",
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
			engine := deps.buildEngine(bus)

			sched := scheduler.New(scheduler.Options{Log: logger, Jitter: 5 * time.Second})
			schedulables := automations.Schedulables(deps.profile)
			out := cmd.OutOrStdout()
			for _, s := range schedulables {
				a := s.Automation
				job := scheduler.Job{
					Name: a.Name(), Schedule: s.Schedule, CatchUp: s.CatchUp,
					Run: func(ctx context.Context) error {
						_, runErr := engine.Run(ctx, a, false)
						return runErr
					},
				}
				if err := sched.Add(job); err != nil {
					fmt.Fprintf(out, "⚠ skip %s: %v\n", a.Name(), err)
					continue
				}
				fmt.Fprintf(out, "scheduled %-18s %s\n", a.Name(), s.Schedule)
			}
			if len(sched.Jobs()) == 0 {
				fmt.Fprintln(out, "no automations enabled/permitted; nothing to schedule")
				if once {
					return nil
				}
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			sched.Start(ctx)
			sched.CatchUp(ctx)
			fmt.Fprintln(out, "daemon running — press Ctrl-C to stop")

			if once {
				// Test/inspection mode: don't block.
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
	return cmd
}
