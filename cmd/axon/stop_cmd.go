package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func newStopCmd(gf *globalFlags) *cobra.Command {
	var timeoutSec int
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon for the active profile (SIGTERM via its pidfile)",
		Long: "Signal the daemon started by `axon start` to shut down gracefully. It reads\n" +
			"the profile's pidfile, sends SIGTERM and waits for the process to exit. A\n" +
			"stale pidfile (no such process) is cleaned up.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, err := loadProfileDeps(gf, false)
			if err != nil {
				return err
			}
			defer deps.close()
			out := cmd.OutOrStdout()
			dataDir := deps.paths.DataDir

			pid, err := readPidFile(dataDir)
			if err != nil {
				return fmt.Errorf("no running daemon for profile %q (no pidfile) — is it started?", deps.name)
			}
			if !processAlive(pid) {
				removePidFile(dataDir)
				fmt.Fprintf(out, "no running daemon (stale pidfile for pid %d removed)\n", pid)
				return nil
			}
			if err := signalStop(pid); err != nil {
				return fmt.Errorf("signal pid %d: %w", pid, err)
			}
			fmt.Fprintf(out, "sent stop signal to pid %d; waiting…\n", pid)

			deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
			for time.Now().Before(deadline) {
				if !processAlive(pid) {
					removePidFile(dataDir)
					fmt.Fprintf(out, "daemon (pid %d) stopped\n", pid)
					return nil
				}
				time.Sleep(200 * time.Millisecond)
			}
			return fmt.Errorf("daemon (pid %d) did not exit within %ds — it may still be shutting down", pid, timeoutSec)
		},
	}
	cmd.Flags().IntVar(&timeoutSec, "timeout", 10, "seconds to wait for the daemon to exit")
	return cmd
}
