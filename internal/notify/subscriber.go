package notify

import (
	"context"
	"log/slog"
	"time"

	"github.com/jandro-es/axon/internal/events"
)

const (
	queueCap    = 64 // pending notifications before dropping
	rateBurst   = 10 // deliveries per rateWindow
	rateWindow  = time.Minute
	sendTimeout = 10 * time.Second
)

// Run subscribes to the bus and delivers matching events, mirroring
// dashboard.PersistEvents: it returns on context cancellation or bus close,
// and never propagates an error.
//
// events.Bus.Publish DROPS rather than blocks on a slow subscriber, so a hung
// POST would silently lose events. Delivery therefore runs on its own
// goroutine behind a bounded queue: the bus-facing loop never waits on HTTP.
// Drops are counted and logged — a silent cap reads as "nothing happened",
// which is the failure this feature exists to prevent.
func Run(ctx context.Context, bus *events.Bus, kinds []string, n Notifier, log *slog.Logger) {
	run(ctx, bus, kinds, n, log, rateWindow/rateBurst)
}

// run is Run with an injectable delivery interval. Tests drive it with a tiny
// interval: the production 6s spacing (a minute across ten deliveries) would
// otherwise make every multi-notification test either slow or flaky.
func run(ctx context.Context, bus *events.Bus, kinds []string, n Notifier, log *slog.Logger, interval time.Duration) {
	if bus == nil || n == nil || len(kinds) == 0 {
		return
	}
	want := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		want[k] = true
	}
	queue := make(chan Note, queueCap)
	go deliver(ctx, queue, n, log, interval)

	sub := bus.Subscribe()
	defer sub.Close()
	dropped := 0
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			if !want[e.Kind] {
				continue
			}
			select {
			case queue <- noteFor(e):
			default:
				dropped++
				log.Warn("notify: queue full, dropping notification",
					"kind", e.Kind, "dropped_total", dropped)
			}
		}
	}
}

// noteFor projects an event onto the wire shape. Event.Data is deliberately
// absent: its fields are arbitrary and emitter-defined (ADR-041).
func noteFor(e events.Event) Note {
	return Note{Kind: e.Kind, Level: e.Level, Title: e.Kind, Body: e.Message}
}

// deliver drains the queue at a bounded rate. A send failure is logged and
// discarded — never retried, never propagated.
func deliver(ctx context.Context, queue <-chan Note, n Notifier, log *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case note, ok := <-queue:
			if !ok {
				return
			}
			sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
			if err := n.Send(sendCtx, note); err != nil {
				log.Warn("notify: delivery failed", "kind", note.Kind, "err", err)
			}
			cancel()
			// Rate limit AFTER the send, so the first notification is
			// immediate and a burst is spread rather than delayed wholesale.
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
}
