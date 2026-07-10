package actions

import (
	"testing"
	"time"
)

func day(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func TestBucketPrecedence(t *testing.T) {
	today := day("2026-07-10")
	cases := []struct {
		name string
		a    Action
		want string
	}{
		{"done wins", Action{State: StateDone, Due: "2026-07-01"}, "done"},
		{"cancelled", Action{State: StateCancelled}, "cancelled"},
		{"someday over overdue", Action{State: StateOpen, Tags: []string{"someday"}, Due: "2026-07-01"}, "someday"},
		{"waiting over overdue", Action{State: StateOpen, Tags: []string{"waiting"}, Due: "2026-07-01"}, "waiting"},
		{"overdue", Action{State: StateOpen, Due: "2026-07-09"}, "overdue"},
		{"today", Action{State: StateOpen, Due: "2026-07-10"}, "today"},
		{"scheduled future start", Action{State: StateOpen, Start: "2026-07-20"}, "scheduled"},
		{"scheduled future sched", Action{State: StateOpen, Scheduled: "2026-07-20"}, "scheduled"},
		{"next (no dates)", Action{State: StateOpen}, "next"},
		{"next (future due only)", Action{State: StateOpen, Due: "2026-07-20"}, "next"},
	}
	for _, c := range cases {
		if got := Bucket(c.a, today); got != c.want {
			t.Errorf("%s: Bucket=%q want %q", c.name, got, c.want)
		}
	}
}
