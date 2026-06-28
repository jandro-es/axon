package events

import (
	"sync"
	"time"
)

// subBuffer is the per-subscriber channel depth. Slow subscribers drop events
// rather than blocking publishers — observability must never stall real work.
const subBuffer = 64

// Bus is an in-process publish/subscribe hub. Publishers never block on slow or
// absent subscribers. It is safe for concurrent use.
type Bus struct {
	mu     sync.RWMutex
	subs   map[int]chan Event
	nextID int
	closed bool
	// now is injectable so tests can supply deterministic timestamps without
	// reaching for the wall clock.
	now func() time.Time
}

// NewBus constructs an empty bus using the wall clock for event timestamps.
func NewBus() *Bus {
	return &Bus{subs: make(map[int]chan Event), now: time.Now}
}

// Subscription is a handle to a stream of events. Call Close to unsubscribe.
type Subscription struct {
	C   <-chan Event
	id  int
	bus *Bus
}

// Subscribe registers a new subscriber and returns its subscription. Events
// published after this call are delivered on Subscription.C.
func (b *Bus) Subscribe() *Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	ch := make(chan Event, subBuffer)
	if b.subs == nil {
		b.subs = make(map[int]chan Event)
	}
	b.subs[id] = ch
	return &Subscription{C: ch, id: id, bus: b}
}

// Close unsubscribes and releases the subscription's channel. Safe to call more
// than once.
func (s *Subscription) Close() {
	if s.bus == nil {
		return
	}
	s.bus.unsubscribe(s.id)
	s.bus = nil
}

func (b *Bus) unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(ch)
	}
}

// Publish stamps the event's timestamp (if unset) and fans it out to every
// current subscriber without blocking. A subscriber whose buffer is full drops
// this event. Returns the number of subscribers the event was delivered to.
func (b *Bus) Publish(e Event) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return 0
	}
	if e.TS.IsZero() {
		e.TS = b.now()
	}
	delivered := 0
	for _, ch := range b.subs {
		select {
		case ch <- e:
			delivered++
		default:
			// Drop: never block a publisher on a slow subscriber.
		}
	}
	return delivered
}

// Close shuts the bus down and closes all subscriber channels. Subsequent
// Publish calls are no-ops.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, ch := range b.subs {
		delete(b.subs, id)
		close(ch)
	}
}
