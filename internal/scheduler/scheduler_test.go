package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestAddRejectsBadSchedule(t *testing.T) {
	s := New(Options{})
	if err := s.Add(Job{Name: "x", Schedule: "not a cron"}); err == nil {
		t.Error("expected error for invalid cron expression")
	}
	if err := s.Add(Job{Name: "y", Schedule: ""}); err == nil {
		t.Error("expected error for empty schedule")
	}
}

func TestAddValidScheduleRegistersJob(t *testing.T) {
	s := New(Options{})
	if err := s.Add(Job{Name: "ok", Schedule: "0 9 * * *", Run: func(ctx context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if len(s.Jobs()) != 1 {
		t.Errorf("jobs = %d, want 1", len(s.Jobs()))
	}
}

func TestCatchUpRunsRunOnceJobs(t *testing.T) {
	s := New(Options{})
	var ran int32
	_ = s.Add(Job{Name: "once", Schedule: "0 9 * * *", CatchUp: CatchUpRunOnce,
		Run: func(ctx context.Context) error { atomic.AddInt32(&ran, 1); return nil }})
	_ = s.Add(Job{Name: "skip", Schedule: "0 9 * * *", CatchUp: CatchUpSkip,
		Run: func(ctx context.Context) error { atomic.AddInt32(&ran, 100); return nil }})

	s.CatchUp(context.Background())
	if atomic.LoadInt32(&ran) != 1 {
		t.Errorf("catch-up ran the wrong jobs: counter = %d, want 1 (only run-once)", ran)
	}
}

func TestFireRunsJobWithJitter(t *testing.T) {
	s := New(Options{Jitter: time.Millisecond, Seed: 1})
	done := make(chan struct{})
	s.runCtx = context.Background()
	s.fire(Job{Name: "j", Run: func(ctx context.Context) error { close(done); return nil }})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("job did not run")
	}
}
