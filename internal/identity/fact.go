package identity

import (
	"regexp"
	"strings"
)

// Fact is the parsed view of one axon:memory line (ADR-028). Struck marks a
// tombstoned (superseded) fact; ValidUntil/SupersededBy are set only when Struck.
// Source is stored raw as written (a [[wikilink]] or a plain token) so
// ParseFact(FormatEntry(e)) round-trips.
type Fact struct {
	Text         string
	Kind         string // fact|decision|lesson|preference|"" (untyped)
	Source       string
	ValidFrom    string // YYYY-MM-DD (the leading date), "" if none
	ValidUntil   string // YYYY-MM-DD or "" (open)
	SupersededBy string // new-fact text (quotes sanitized) or "" (unknown/none)
	Struck       bool
}

var (
	factUntilRe      = regexp.MustCompile(`^\(until (\d{4}-\d{2}-\d{2}); superseded by "(.*)"\)$`)
	factSupersededRe = regexp.MustCompile(`^\(superseded (\d{4}-\d{2}-\d{2})\)$`)
	factDateRe       = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// ParseFact parses one "- …" memory line into a Fact. Legacy lines (no [fact]
// kind, bare "(superseded DATE)" tombstones) parse correctly. Returns ok=false
// for a non-entry line (blank, or without the "- " bullet prefix).
func ParseFact(line string) (Fact, bool) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "- ") {
		return Fact{}, false
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "- "))
	if s == "" {
		return Fact{}, false
	}
	var f Fact
	if strings.HasPrefix(s, "~~") {
		inner, after, found := strings.Cut(s[len("~~"):], "~~")
		if !found {
			return Fact{}, false
		}
		annotation := strings.TrimSpace(after)
		f.Struck = true
		if m := factUntilRe.FindStringSubmatch(annotation); m != nil {
			f.ValidUntil, f.SupersededBy = m[1], m[2]
		} else if m := factSupersededRe.FindStringSubmatch(annotation); m != nil {
			f.ValidUntil = m[1]
		}
		parseFactBody(inner, &f)
		return f, true
	}
	parseFactBody(s, &f)
	return f, true
}

// parseFactBody fills Text/Kind/Source/ValidFrom from an open-fact body of the
// form "DATE — text [kind] (source: SRC)". Every trailing element is optional.
func parseFactBody(body string, f *Fact) {
	body = strings.TrimSpace(body)
	if i := strings.Index(body, " — "); i >= 0 {
		if cand := strings.TrimSpace(body[:i]); factDateRe.MatchString(cand) {
			f.ValidFrom = cand
			body = strings.TrimSpace(body[i+len(" — "):])
		}
	}
	if i := strings.LastIndex(body, " (source:"); i >= 0 {
		if end := strings.LastIndex(body, ")"); end > i {
			f.Source = strings.TrimSpace(body[i+len(" (source:") : end])
			body = strings.TrimSpace(body[:i])
		}
	}
	if strings.HasSuffix(body, "]") {
		if i := strings.LastIndex(body, " ["); i >= 0 {
			f.Kind = body[i+len(" [") : len(body)-1]
			body = strings.TrimSpace(body[:i])
		}
	}
	f.Text = strings.TrimSpace(body)
}
