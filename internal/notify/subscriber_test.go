package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/events"
)

type fakeNotifier struct {
	mu    sync.Mutex
	sent  []Note
	err   error
	block chan struct{} // when non-nil, Send waits on it
}

func (f *fakeNotifier) Send(ctx context.Context, n Note) error {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, n)
	return f.err
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeNotifier) first() Note {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent[0]
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// eventually polls until cond holds or the deadline passes — delivery is
// asynchronous by design, so a bare assert would be flaky.
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

func TestSubscriberDeliversSubscribedKindsOnly(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()
	f := &fakeNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go run(ctx, bus, []string{"automation.fail"}, f, quietLog(), time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	bus.Publish(events.Event{Kind: "automation.fail", Level: events.LevelError, Message: "capture failed"})
	bus.Publish(events.Event{Kind: "ingest.done", Level: events.LevelInfo, Message: "ingested a page"})

	eventually(t, func() bool { return f.count() == 1 }, "the subscribed kind was not delivered")
	time.Sleep(50 * time.Millisecond)
	if f.count() != 1 {
		t.Fatalf("an unsubscribed kind was delivered: %d notes", f.count())
	}
	if got := f.first(); got.Kind != "automation.fail" || got.Body != "capture failed" {
		t.Fatalf("wrong note: %+v", got)
	}
}

// Event.Data must never reach the payload.
func TestSubscriberNeverSendsEventData(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()
	f := &fakeNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go run(ctx, bus, []string{"automation.fail"}, f, quietLog(), time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	bus.Publish(events.Event{
		Kind: "automation.fail", Message: "failed",
		Data: map[string]any{"secret_path": "/Users/me/.ssh/id_rsa"},
	})
	eventually(t, func() bool { return f.count() == 1 }, "not delivered")
	got := f.first()
	if got.Body != "failed" {
		t.Fatalf("body must be the message alone, got %q", got.Body)
	}
	if got.Title != "automation.fail" {
		t.Fatalf("title must be the kind, got %q", got.Title)
	}
}

// A slow notifier must never block the publisher.
func TestSubscriberDoesNotBlockThePublisher(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()
	block := make(chan struct{})
	f := &fakeNotifier{block: block}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go run(ctx, bus, []string{"automation.fail"}, f, quietLog(), time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			bus.Publish(events.Event{Kind: "automation.fail", Message: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publishing blocked on a stuck notifier")
	}
	close(block)
}

// A Send error must not stop the loop.
func TestSubscriberSurvivesSendErrors(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()
	f := &fakeNotifier{err: errors.New("host down")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go run(ctx, bus, []string{"automation.fail"}, f, quietLog(), time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	bus.Publish(events.Event{Kind: "automation.fail", Message: "one"})
	eventually(t, func() bool { return f.count() >= 1 }, "first not attempted")
	bus.Publish(events.Event{Kind: "automation.fail", Message: "two"})
	eventually(t, func() bool { return f.count() >= 2 }, "the loop stopped after a send error")
}

func TestSubscriberStopsOnContextCancel(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()
	f := &fakeNotifier{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { run(ctx, bus, []string{"automation.fail"}, f, quietLog(), time.Millisecond); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return on context cancellation")
	}
}

func TestSubscriberIsNoOpWhenUnconfigured(t *testing.T) {
	bus := events.NewBus()
	defer bus.Close()
	f := &fakeNotifier{}
	done := make(chan struct{})
	go func() { Run(context.Background(), bus, nil, f, quietLog()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run with no kinds must return immediately")
	}
}

// The rate limiter must actually space deliveries — the injectable interval
// exists for test speed, not to leave the real behaviour unverified.
func TestDeliverSpacesSendsByTheInterval(t *testing.T) {
	f := &fakeNotifier{}
	queue := make(chan Note, 4)
	for i := 0; i < 3; i++ {
		queue <- Note{Kind: "automation.fail", Body: "x"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const interval = 60 * time.Millisecond
	start := time.Now()
	go deliver(ctx, queue, f, quietLog(), interval)

	eventually(t, func() bool { return f.count() == 3 }, "all three should eventually deliver")
	// Three sends spaced by one interval each: at least two full gaps.
	if elapsed := time.Since(start); elapsed < 2*interval {
		t.Fatalf("deliveries were not rate limited: 3 sends in %v (want >= %v)", elapsed, 2*interval)
	}
}

// Production constants must stay sane: ten deliveries per minute.
func TestProductionRateConstants(t *testing.T) {
	if got := rateWindow / rateBurst; got != 6*time.Second {
		t.Fatalf("delivery interval = %v, want 6s (10/minute)", got)
	}
	if queueCap < 1 {
		t.Fatal("queueCap must be positive")
	}
}
