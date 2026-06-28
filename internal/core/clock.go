package core

import "time"

// nowStamp returns the current UTC time as an ISO-8601 date-time string, the
// format AXON uses for derived timestamp columns (last_indexed, etc.).
func nowStamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
