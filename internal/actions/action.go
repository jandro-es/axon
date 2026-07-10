// Package actions parses Obsidian-Tasks-grammar checkbox lines into structured
// Actions and computes their GTD status. Pure leaf: stdlib only. It is the one
// task parser in AXON — reindex, the CLI, and later slices all read it.
package actions

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
)

// State is the checkbox-derived, date-independent lifecycle of an action.
type State string

const (
	StateOpen      State = "open"
	StateDone      State = "done"
	StateCancelled State = "cancelled"
)

// Action is one parsed checkbox line. Line-local fields come from Parse;
// SourcePath/LineNo/Section/Archived are stamped by Extract.
type Action struct {
	SourcePath string   `json:"source_path"`
	LineNo     int      `json:"line_no"`
	Section    string   `json:"section,omitempty"`
	Text       string   `json:"text"`
	Raw        string   `json:"raw"`
	State      State    `json:"state"`
	Checkbox   string   `json:"checkbox"`
	Priority   string   `json:"priority,omitempty"`
	Due        string   `json:"due,omitempty"`
	Scheduled  string   `json:"scheduled,omitempty"`
	Start      string   `json:"start,omitempty"`
	DoneDate   string   `json:"done_date,omitempty"`
	Project    string   `json:"project,omitempty"`
	Contexts   []string `json:"contexts,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Archived   bool     `json:"archived,omitempty"`
}

var (
	checkboxRe = regexp.MustCompile(`^\s*[-*+] \[(.)\] (.*)$`)
	dueRe      = regexp.MustCompile(`\x{1F4C5}\s*(\d{4}-\d{2}-\d{2})`) // 📅
	schedRe    = regexp.MustCompile(`\x{23F3}\s*(\d{4}-\d{2}-\d{2})`)  // ⏳
	startRe    = regexp.MustCompile(`\x{1F6EB}\s*(\d{4}-\d{2}-\d{2})`) // 🛫
	doneRe     = regexp.MustCompile(`\x{2705}\s*(\d{4}-\d{2}-\d{2})`)  // ✅
	cancelRe   = regexp.MustCompile(`\x{274C}\s*(\d{4}-\d{2}-\d{2})`)  // ❌ (tolerated, value unused)
	contextRe  = regexp.MustCompile(`(?:^|\s)@(\w[\w/-]*)`)
	tagRe      = regexp.MustCompile(`(?:^|\s)#([\w/][\w/-]*)`)
	wikilinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	wsRe       = regexp.MustCompile(`\s+`)
)

var priorityEmoji = []struct{ glyph, word string }{
	{"\U0001F53A", "highest"}, // 🔺
	{"⏫", "high"},        // ⏫
	{"\U0001F53C", "medium"},  // 🔼
	{"⏬", "lowest"},      // ⏬
	{"\U0001F53D", "low"},     // 🔽
}

// Parse turns one line into an Action. ok=false for a non-checkbox line.
func Parse(line string) (Action, bool) {
	m := checkboxRe.FindStringSubmatch(line)
	if m == nil {
		return Action{}, false
	}
	a := Action{Raw: line, Checkbox: m[1]}
	switch m[1] {
	case "x", "X":
		a.State = StateDone
	case "-":
		a.State = StateCancelled
	default:
		a.State = StateOpen // " " and any unknown marker (tolerant)
	}
	body := m[2]
	body = extractDate(body, dueRe, &a.Due)
	body = extractDate(body, schedRe, &a.Scheduled)
	body = extractDate(body, startRe, &a.Start)
	body = extractDate(body, doneRe, &a.DoneDate)
	body = cancelRe.ReplaceAllString(body, "")
	for _, p := range priorityEmoji {
		if strings.Contains(body, p.glyph) {
			a.Priority = p.word
			body = strings.ReplaceAll(body, p.glyph, "")
			break
		}
	}
	for _, cm := range contextRe.FindAllStringSubmatch(body, -1) {
		a.Contexts = append(a.Contexts, cm[1])
	}
	for _, tm := range tagRe.FindAllStringSubmatch(body, -1) {
		a.Tags = append(a.Tags, tm[1])
	}
	if wm := wikilinkRe.FindStringSubmatch(body); wm != nil {
		a.Project = linkTarget(wm[1])
	}
	a.Text = strings.TrimSpace(wsRe.ReplaceAllString(body, " "))
	return a, true
}

// Hash is the stable identity: sha256(source_path + "\n" + normalized body),
// where the body EXCLUDES the checkbox marker (so [ ]→[x] keeps identity) but
// includes dates/text (so a reschedule is a new identity — the T3 stale-hash
// contract). SourcePath must be set (Extract does so).
func (a Action) Hash() string {
	body := a.Raw
	if m := checkboxRe.FindStringSubmatch(body); m != nil {
		body = m[2]
	}
	norm := strings.TrimSpace(wsRe.ReplaceAllString(body, " "))
	sum := sha256.Sum256([]byte(a.SourcePath + "\n" + norm))
	return hex.EncodeToString(sum[:])
}

// Bucket resolves the single GTD bucket by precedence:
// done > cancelled > someday > waiting > overdue > today > scheduled > next.
// Date fields are compared lexically against today (YYYY-MM-DD). Read-time only —
// never persisted, so it can't go stale at midnight.
func Bucket(a Action, today time.Time) string {
	switch a.State {
	case StateDone:
		return "done"
	case StateCancelled:
		return "cancelled"
	}
	t := today.Format("2006-01-02")
	switch {
	case hasTag(a.Tags, "someday"):
		return "someday"
	case hasTag(a.Tags, "waiting"):
		return "waiting"
	case a.Due != "" && a.Due < t:
		return "overdue"
	case a.Due == t:
		return "today"
	case a.Start > t || a.Scheduled > t:
		return "scheduled"
	default:
		return "next"
	}
}

func hasTag(tags []string, want string) bool {
	for _, tg := range tags {
		if strings.EqualFold(tg, want) {
			return true
		}
	}
	return false
}

func extractDate(body string, re *regexp.Regexp, dst *string) string {
	if m := re.FindStringSubmatch(body); m != nil {
		*dst = m[1]
		body = re.ReplaceAllString(body, "")
	}
	return body
}

func linkTarget(s string) string {
	if i := strings.IndexByte(s, '|'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
