package automations

import "time"

// schedItem is one pair's spaced-repetition state (R9, FR-151). Persisted as
// JSON in automation_state; derived operational state, S9-safe.
type schedItem struct {
	Rung        int    `json:"rung"`           // index into the interval ladder, clamped
	Due         string `json:"due"`            // YYYY-MM-DD; surface only when Due <= today
	LastOutcome string `json:"last,omitempty"` // date of the last applied resolution (idempotency anchor)
}

// resurfaceSchedule maps a pair key (see pairKey) to its schedule.
type resurfaceSchedule map[string]schedItem

// ladderDays converts a weeks ladder to days, defaulting to [7,14,28,56,112].
func ladderDays(weeks []int) []int {
	if len(weeks) == 0 {
		return []int{7, 14, 28, 56, 112}
	}
	days := make([]int, len(weeks))
	for i, w := range weeks {
		days[i] = w * 7
	}
	return days
}

// clampRung bounds a rung to the ladder's last index.
func clampRung(rung, n int) int {
	if rung < 0 {
		return 0
	}
	if rung >= n {
		return n - 1
	}
	return rung
}

// dueAfter returns the YYYY-MM-DD due date `ladder[rung]` days after anchor.
func dueAfter(anchor time.Time, rung int, ladder []int) string {
	rung = clampRung(rung, len(ladder))
	return anchor.AddDate(0, 0, ladder[rung]).Format("2006-01-02")
}

// isDue reports whether an item's interval has elapsed by today (string compare
// is valid for YYYY-MM-DD). An empty Due is always due.
func isDue(it schedItem, today string) bool {
	return it.Due == "" || it.Due <= today
}

// advance moves a pair's schedule after a resolution: accept lengthens more
// (rung+2) than dismiss (rung+1), so intervals demonstrably grow on acceptance.
// Due is anchored on the resolution date, not the run date.
func advance(cur schedItem, accepted bool, resolutionDate string, ladder []int) schedItem {
	step := 1
	if accepted {
		step = 2
	}
	cur.Rung = clampRung(cur.Rung+step, len(ladder))
	anchor, err := time.Parse("2006-01-02", resolutionDate)
	if err != nil {
		anchor = time.Now().UTC() // defensive: unparseable date → schedule off today
	}
	cur.Due = dueAfter(anchor, cur.Rung, ladder)
	cur.LastOutcome = resolutionDate
	return cur
}
