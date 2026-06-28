package tokens

import (
	"fmt"
	"time"
)

// dayPeriod is the identifier for the current day window (UTC calendar day).
func dayPeriod(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// weekPeriod is the identifier for the current week window (ISO year-week), so
// the window rolls over predictably regardless of month boundaries.
func weekPeriod(t time.Time) string {
	year, week := t.UTC().ISOWeek()
	return fmt.Sprintf("%04d-W%02d", year, week)
}
