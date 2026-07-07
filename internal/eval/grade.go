package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Verdict is the outcome of grading one case.
type Verdict struct {
	Pass      bool
	Escalated bool   // the answer came from a model other than the target (fall-forward)
	Reason    string // human-readable why
}

// gradeClassify grades a classify case deterministically: semantic JSON equality
// when ExpectJSON is set, else normalized text equality.
func gradeClassify(c Case, got string) Verdict {
	if c.Grade.ExpectJSON != "" {
		wantN, err := canonicalJSON([]byte(c.Grade.ExpectJSON))
		if err != nil {
			return Verdict{Reason: fmt.Sprintf("fixture expect_json invalid: %v", err)}
		}
		gotN, err := canonicalJSON([]byte(got))
		if err != nil {
			return Verdict{Reason: fmt.Sprintf("candidate is not valid JSON: %v", err)}
		}
		if bytes.Equal(wantN, gotN) {
			return Verdict{Pass: true}
		}
		return Verdict{Reason: fmt.Sprintf("json mismatch: want %s, got %s", wantN, gotN)}
	}
	if normalizeText(got) == normalizeText(c.Grade.ExpectText) {
		return Verdict{Pass: true}
	}
	return Verdict{Reason: fmt.Sprintf("text mismatch: want %q, got %q", c.Grade.ExpectText, got)}
}

// canonicalJSON unmarshals then re-marshals so key order and insignificant
// whitespace do not affect equality (Go sorts map keys on Marshal).
func canonicalJSON(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// normalizeText trims, collapses internal whitespace runs to one space, and
// lowercases — so "  HIGH\n" == "high".
func normalizeText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// judgeSystem pins the Claude judge to a strict JSON verdict. Because the judge
// is Claude, this schema is reliable (guarded further by ValidateOutput in the
// runner).
const judgeSystem = `You are a strict grader. Given a rubric and a candidate answer,
decide if the candidate satisfies the rubric. Respond with JSON only, no prose:
{"pass": <true|false>, "reason": "<one short sentence>"}`

// mustInclude reports whether every anchor substring appears in got. On failure
// it returns the first missing anchor for the verdict reason.
func mustInclude(anchors []string, got string) (bool, string) {
	for _, a := range anchors {
		if !strings.Contains(got, a) {
			return false, a
		}
	}
	return true, ""
}

// judgePrompt is the user turn handed to the judge: the rubric plus the
// candidate answer to grade.
func judgePrompt(rubric, candidate string) string {
	return fmt.Sprintf("Rubric:\n%s\n\nCandidate answer:\n%s", rubric, candidate)
}

// parseJudge extracts and parses the judge's {"pass","reason"} verdict, tolerant
// of surrounding prose or ```json fences. A malformed verdict is an error (the
// runner scores that case failed), never a panic.
func parseJudge(raw string) (bool, string, error) {
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end < start {
		return false, "", fmt.Errorf("no JSON object in judge output")
	}
	var v struct {
		Pass   bool   `json:"pass"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &v); err != nil {
		return false, "", fmt.Errorf("judge output not valid JSON: %w", err)
	}
	return v.Pass, v.Reason, nil
}

// validateJudgeOutput is the ValidateOutput guard for the judge call: it fails
// the call when the response is not a parseable verdict.
func validateJudgeOutput(s string) error {
	_, _, err := parseJudge(s)
	return err
}
