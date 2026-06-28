package ingestion

import (
	"fmt"
	"regexp"
)

// redactionPlaceholder replaces any matched sensitive content.
const redactionPlaceholder = "[REDACTED]"

// Redactor applies the profile's redaction rules to text BEFORE it could reach
// the model or be persisted (NFR-05). Patterns are profile policy regexes.
type Redactor struct {
	patterns []*regexp.Regexp
}

// NewRedactor compiles the rules. A bad pattern is a configuration error.
func NewRedactor(rules []string) (*Redactor, error) {
	r := &Redactor{}
	for _, rule := range rules {
		re, err := regexp.Compile(rule)
		if err != nil {
			return nil, fmt.Errorf("redaction rule %q: %w", rule, err)
		}
		r.patterns = append(r.patterns, re)
	}
	return r, nil
}

// Redact replaces every match with the placeholder and reports whether anything
// matched (so the source can be marked status=redacted).
func (r *Redactor) Redact(text string) (string, bool) {
	matched := false
	for _, re := range r.patterns {
		if re.MatchString(text) {
			matched = true
			text = re.ReplaceAllString(text, redactionPlaceholder)
		}
	}
	return text, matched
}
