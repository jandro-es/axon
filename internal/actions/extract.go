package actions

import (
	"regexp"
	"strings"
)

var headingRe = regexp.MustCompile(`^#{1,6}\s+(.*\S)\s*$`)

// Extract parses every checkbox line in a note body. It tracks the nearest
// heading as Section, skips fenced code blocks and the axon:actions projection
// block (constitution §3), and stamps note-level fields on each Action.
func Extract(sourcePath, body string, archived bool) []Action {
	var out []Action
	section := ""
	inFence, fenceTok := false, ""
	inActions := false
	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.Contains(trimmed, "<!-- axon:actions:start -->"):
			inActions = true
			continue
		case strings.Contains(trimmed, "<!-- axon:actions:end -->"):
			inActions = false
			continue
		}
		if inActions {
			continue
		}
		if tok := fenceToken(trimmed); tok != "" {
			switch {
			case !inFence:
				inFence, fenceTok = true, tok
			case tok == fenceTok:
				inFence, fenceTok = false, ""
			}
			continue
		}
		if inFence {
			continue
		}
		if hm := headingRe.FindStringSubmatch(line); hm != nil {
			section = hm[1]
			continue
		}
		if a, ok := Parse(line); ok {
			a.SourcePath = sourcePath
			a.LineNo = i
			a.Section = section
			a.Archived = archived
			out = append(out, a)
		}
	}
	return out
}

func fenceToken(trimmed string) string {
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return "```"
	case strings.HasPrefix(trimmed, "~~~"):
		return "~~~"
	default:
		return ""
	}
}
