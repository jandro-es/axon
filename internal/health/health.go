// Package health scores how well an AXON vault ("second brain") is doing. It
// derives a 0..100 score with a letter grade from purely local state — index &
// link integrity, automation reliability, and knowledge freshness — spending no
// tokens and making no model call. It is a leaf over the db read-layer, config
// and the automation catalog; nothing in core depends on it.
package health

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/jandro-es/axon/internal/automations"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

// Dimension is one component of the vault health score.
type Dimension struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Score  int    `json:"score"` // 0..100
	Detail string `json:"detail"`
}

// Report is the overall "how is my second brain doing" snapshot: an aggregate
// 0..100 score with a letter grade, plus the per-dimension breakdown.
type Report struct {
	Score      int         `json:"score"` // 0..100 (mean of dimensions)
	Grade      string      `json:"grade"` // A..F
	Dimensions []Dimension `json:"dimensions"`
}

// Compute builds the health report as of now.
func Compute(ctx context.Context, database *sql.DB, profile config.Profile) (Report, error) {
	return ComputeAt(ctx, database, profile, time.Now())
}

// ComputeAt is Compute with an injectable clock (for deterministic tests).
func ComputeAt(ctx context.Context, database *sql.DB, profile config.Profile, now time.Time) (Report, error) {
	integrity, err := integrityDimension(ctx, database)
	if err != nil {
		return Report{}, err
	}
	reliability, err := reliabilityDimension(ctx, database, profile)
	if err != nil {
		return Report{}, err
	}
	freshness, err := freshnessDimension(ctx, database, now)
	if err != nil {
		return Report{}, err
	}

	dims := []Dimension{integrity, reliability, freshness}
	sum := 0
	for _, d := range dims {
		sum += d.Score
	}
	score := sum / len(dims)
	return Report{Score: score, Grade: grade(score), Dimensions: dims}, nil
}

// integrityDimension scores index + link health: embedding coverage and the
// share of wikilinks that actually resolve.
func integrityDimension(ctx context.Context, q *sql.DB) (Dimension, error) {
	d := Dimension{Key: "integrity", Label: "Index & link integrity"}
	notes, err := db.CountNotes(ctx, q)
	if err != nil {
		return d, err
	}
	if notes == 0 {
		d.Score = 100
		d.Detail = "vault is empty — nothing indexed yet"
		return d, nil
	}
	chunks, err := db.CountChunks(ctx, q)
	if err != nil {
		return d, err
	}
	vectors, err := db.CountVectors(ctx, q)
	if err != nil {
		return d, err
	}
	links, err := db.CountLinks(ctx, q)
	if err != nil {
		return d, err
	}
	broken, err := db.CountBrokenWikilinks(ctx, q)
	if err != nil {
		return d, err
	}

	coverage := 1.0
	if chunks > 0 {
		coverage = float64(vectors) / float64(chunks)
	}
	brokenRatio := 0.0
	if links > 0 {
		brokenRatio = float64(broken) / float64(links)
	}
	d.Score = clampScore(60*coverage + 40*(1-brokenRatio))
	d.Detail = fmt.Sprintf("%d notes · %d%% chunks embedded · %d broken link(s)",
		notes, int(math.Round(coverage*100)), broken)
	return d, nil
}

// reliabilityDimension scores the recent behaviour of enabled automations: a
// failed last run hurts, a healthy (ok/skipped) one helps, never-run is unknown.
func reliabilityDimension(ctx context.Context, q *sql.DB, profile config.Profile) (Dimension, error) {
	d := Dimension{Key: "reliability", Label: "Automation reliability"}
	var enabled []automations.Info
	for _, info := range automations.Catalog(profile) {
		if info.Enabled {
			enabled = append(enabled, info)
		}
	}
	if len(enabled) == 0 {
		d.Score = 100
		d.Detail = "no automations enabled"
		return d, nil
	}

	var healthy, failing, neverRun int
	total := 0.0
	for _, info := range enabled {
		rec, found, err := db.LastRun(ctx, q, info.Name)
		if err != nil {
			return d, err
		}
		switch {
		case !found:
			neverRun++
			total += 0.5 // unknown — neither credit nor full penalty
		case rec.Status == db.RunFailed:
			failing++
			total += 0.0
		default: // ok | skipped | dry-run
			healthy++
			total += 1.0
		}
	}
	d.Score = clampScore(total / float64(len(enabled)) * 100)
	d.Detail = fmt.Sprintf("%d healthy · %d failing · %d never run (of %d enabled)",
		healthy, failing, neverRun, len(enabled))
	return d, nil
}

// freshnessDimension scores how recently the brain was tended: the newest of the
// last ingestion and the last automation finish. A stale, untended vault decays.
func freshnessDimension(ctx context.Context, q *sql.DB, now time.Time) (Dimension, error) {
	d := Dimension{Key: "freshness", Label: "Knowledge freshness"}
	notes, err := db.CountNotes(ctx, q)
	if err != nil {
		return d, err
	}
	lastSource, err := db.LatestSourceFetch(ctx, q)
	if err != nil {
		return d, err
	}
	lastRun, err := db.LatestRunFinish(ctx, q)
	if err != nil {
		return d, err
	}

	latest := laterTimestamp(lastSource, lastRun)
	if latest.IsZero() {
		if notes == 0 {
			d.Score = 100
			d.Detail = "new vault — no activity to age yet"
			return d, nil
		}
		d.Score = 40
		d.Detail = "no ingestion or automation activity recorded yet"
		return d, nil
	}

	age := now.Sub(latest)
	d.Score = freshnessScore(age)
	d.Detail = fmt.Sprintf("last activity %s", humanizeAge(age))
	return d, nil
}

// freshnessScore maps staleness to a 0..100 score with a gentle decay.
func freshnessScore(age time.Duration) int {
	days := age.Hours() / 24
	switch {
	case days < 1:
		return 100
	case days < 3:
		return 90
	case days < 7:
		return 80
	case days < 14:
		return 65
	case days < 30:
		return 50
	case days < 60:
		return 30
	default:
		return 15
	}
}

func laterTimestamp(a, b string) time.Time {
	ta := parseTS(a)
	tb := parseTS(b)
	if tb.After(ta) {
		return tb
	}
	return ta
}

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func humanizeAge(age time.Duration) string {
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%d minute(s) ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%d hour(s) ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%d day(s) ago", int(age.Hours()/24))
	}
}

// clampScore rounds a float to an int in [0,100].
func clampScore(v float64) int {
	n := int(math.Round(v))
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

// grade maps a 0..100 score to a letter grade.
func grade(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 80:
		return "B"
	case score >= 70:
		return "C"
	case score >= 60:
		return "D"
	default:
		return "F"
	}
}
