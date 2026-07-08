package tokens

import (
	"regexp"
	"strconv"
	"strings"
)

// verifyJudgeSystem pins the local judge to emit a single integer 0–10.
const verifyJudgeSystem = "You are a strict evaluator. Judge whether the ASSISTANT ANSWER correctly and faithfully completes the TASK. Reply with ONLY a single integer from 0 to 10, where 10 means fully correct and faithful and 0 means wrong or unfaithful. Output the number and nothing else."

// buildVerifyPrompt renders the judge's system + user prompt from the original
// task (system + messages) and the candidate answer.
func buildVerifyPrompt(system string, msgs []Message, answer string) (string, string) {
	var task strings.Builder
	if system != "" {
		task.WriteString(system)
		task.WriteString("\n\n")
	}
	task.WriteString(joinMessages(msgs))

	var b strings.Builder
	b.WriteString("TASK:\n")
	b.WriteString(task.String())
	b.WriteString("\n\nASSISTANT ANSWER:\n")
	b.WriteString(answer)
	b.WriteString("\n\nSCORE (0-10):")
	return verifyJudgeSystem, b.String()
}

// verifyScoreRe matches the first run of digits in the judge's reply. Mirrors
// rerank.parseScore's pragmatism: the judge is prompted for a bare number, so
// the first integer is the score; out-of-range values clamp.
var verifyScoreRe = regexp.MustCompile(`\d+`)

// parseVerifyScore extracts the first integer from text, clamped to [0,10]; ok
// is false when none is found (→ inconclusive → the caller keeps the local
// answer).
func parseVerifyScore(text string) (int, bool) {
	m := verifyScoreRe.FindString(text)
	if m == "" {
		return 0, false
	}
	n, err := strconv.Atoi(m)
	if err != nil {
		return 0, false
	}
	if n < 0 {
		n = 0
	}
	if n > 10 {
		n = 10
	}
	return n, true
}
