package events

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBusPublishToSubscribers(t *testing.T) {
	b := NewBus()
	defer b.Close()

	s1 := b.Subscribe()
	defer s1.Close()
	s2 := b.Subscribe()
	defer s2.Close()

	n := b.Publish(Event{Kind: "test", Message: "hi"})
	if n != 2 {
		t.Fatalf("delivered to %d subscribers, want 2", n)
	}

	for i, s := range []*Subscription{s1, s2} {
		select {
		case e := <-s.C:
			if e.Kind != "test" {
				t.Errorf("sub %d got kind %q, want test", i, e.Kind)
			}
			if e.TS.IsZero() {
				t.Errorf("sub %d event has zero timestamp; Publish should stamp it", i)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d timed out waiting for event", i)
		}
	}
}

func TestBusTimestampInjectable(t *testing.T) {
	fixed := time.Date(2026, 6, 28, 9, 0, 0, 0, time.UTC)
	b := NewBus()
	b.now = func() time.Time { return fixed }
	defer b.Close()

	s := b.Subscribe()
	defer s.Close()
	b.Publish(Event{Kind: "k"})
	e := <-s.C
	if !e.TS.Equal(fixed) {
		t.Errorf("timestamp = %v, want %v", e.TS, fixed)
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	b := NewBus()
	defer b.Close()
	s := b.Subscribe()
	s.Close()
	if n := b.Publish(Event{Kind: "k"}); n != 0 {
		t.Errorf("delivered to %d subscribers after unsubscribe, want 0", n)
	}
	s.Close() // double close must be safe
}

func TestBusDoesNotBlockOnFullSubscriber(t *testing.T) {
	b := NewBus()
	defer b.Close()
	b.Subscribe() // never drained

	done := make(chan struct{})
	go func() {
		for range subBuffer * 3 {
			b.Publish(Event{Kind: "flood"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full/slow subscriber")
	}
}

func TestPublishAfterCloseIsNoop(t *testing.T) {
	b := NewBus()
	b.Close()
	if n := b.Publish(Event{Kind: "k"}); n != 0 {
		t.Errorf("Publish after Close delivered %d, want 0", n)
	}
}

func TestNewLoggerJSON(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, FormatJSON, "info")
	log.Info("hello", "key", "value")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v (%q)", err, buf.String())
	}
	if entry["msg"] != "hello" || entry["key"] != "value" {
		t.Errorf("unexpected log entry: %v", entry)
	}
}

func TestNewLoggerLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := NewLogger(&buf, FormatText, "warn")
	log.Info("should be filtered")
	log.Warn("should appear")
	out := buf.String()
	if strings.Contains(out, "should be filtered") {
		t.Error("info message leaked past warn level")
	}
	if !strings.Contains(out, "should appear") {
		t.Error("warn message was dropped")
	}
}
