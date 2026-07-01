// Package scheduler is AXON's portable, in-daemon job scheduler (ADR-008): cron
// expressions run automations cross-platform with no OS cron dependency. It adds
// jitter to avoid thundering herds and supports a catch-up policy for jobs
// missed while the daemon was down. Overlap protection and timeouts live in the
// automation engine (per-automation locks), which each job invokes.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// CatchUp policies for runs missed while the daemon was down.
const (
	CatchUpSkip    = "skip"
	CatchUpRunOnce = "run-once"
)

// Job is a scheduled automation invocation.
type Job struct {
	Name     string
	Schedule string // 5-field cron expression
	CatchUp  string // skip | run-once
	Run      func(ctx context.Context) error
}

// Scheduler wraps robfig/cron with jitter and a bounded run context.
type Scheduler struct {
	cron   *cron.Cron
	log    *slog.Logger
	jitter time.Duration
	runCtx context.Context
	mu     sync.Mutex
	rnd    *rand.Rand
	jobs   []Job
}

// Options configure a Scheduler.
type Options struct {
	Log    *slog.Logger
	Jitter time.Duration // max random delay before a job runs (0 disables)
	Seed   int64         // RNG seed for jitter (pass a fixed value for tests)
}

// New constructs a Scheduler.
func New(opts Options) *Scheduler {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{
		cron:   cron.New(),
		log:    log,
		jitter: opts.Jitter,
		rnd:    rand.New(rand.NewSource(opts.Seed)),
	}
}

// Add registers a job on its cron schedule. The job runs through a jitter delay
// and the scheduler's run context (cancelled on Stop).
func (s *Scheduler) Add(job Job) error {
	if job.Schedule == "" {
		return fmt.Errorf("job %q has no schedule", job.Name)
	}
	if _, err := s.cron.AddFunc(job.Schedule, func() { s.fire(job) }); err != nil {
		return fmt.Errorf("schedule %q (%q): %w", job.Name, job.Schedule, err)
	}
	s.mu.Lock()
	s.jobs = append(s.jobs, job)
	s.mu.Unlock()
	return nil
}

// fire runs a single job with jitter and panic-safety.
func (s *Scheduler) fire(job Job) {
	if d := s.jitterDelay(); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-s.ctx().Done():
			return
		}
	}
	s.safeRun(s.ctx(), job, "automation")
}

// safeRun executes a job, converting a panic into a logged error so one broken
// automation can never take the daemon down (used by both scheduled fires and
// startup catch-up).
func (s *Scheduler) safeRun(ctx context.Context, job Job, kind string) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error(kind+" panicked", "job", job.Name, "panic", r)
		}
	}()
	if err := job.Run(ctx); err != nil {
		s.log.Warn(kind+" run error", "job", job.Name, "error", err)
	}
}

func (s *Scheduler) jitterDelay() time.Duration {
	if s.jitter <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Duration(s.rnd.Int63n(int64(s.jitter)))
}

func (s *Scheduler) ctx() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runCtx == nil {
		return context.Background()
	}
	return s.runCtx
}

// CatchUp runs, once and immediately, every registered run-once job — the simple
// interpretation of catching up runs missed while the daemon was down. (skip
// jobs are left for their next scheduled tick.)
func (s *Scheduler) CatchUp(ctx context.Context) {
	for _, job := range s.Jobs() {
		if job.CatchUp == CatchUpRunOnce {
			s.safeRun(ctx, job, "catch-up")
		}
	}
}

// Start begins the schedule. ctx cancels in-flight jitter waits on Stop.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	s.runCtx = ctx
	s.mu.Unlock()
	s.cron.Start()
}

// Stop halts scheduling and returns a context that is done once running jobs
// complete.
func (s *Scheduler) Stop() context.Context {
	return s.cron.Stop()
}

// Jobs returns a copy of the registered jobs (for status/inspection).
func (s *Scheduler) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, len(s.jobs))
	copy(out, s.jobs)
	return out
}
