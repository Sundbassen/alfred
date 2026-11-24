package llm

import (
	"math"
	"strings"
	"unicode"
)

// TextConfidence measures how sure the model was of a string field's value
// in its answer (e.g. a free-text answer), from the generated tokens'
// probabilities. Only tokens with letters or digits count: the JSON
// structure is forced by the schema, and formatting ("*", ".") is
// noise. mean is the geometric mean probability of those tokens; least is
// the probability of the least likely one, which catches a single made-up
// name in an otherwise fluent sentence. ok is false if the field isn't in
// the tokens.
//
// Token probability measures how sure the model is of its wording. For
// short factual answers that tracks correctness reasonably, though small
// models are less well calibrated; tune thresholds on known questions.
func TextConfidence(tokens []Token, field string) (mean, least float64, ok bool) {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteString(t.Text)
	}
	text := b.String()

	// The value of "field": after the colon, between unescaped quotes.
	key := strings.Index(text, `"`+field+`"`)
	if key < 0 {
		return 0, 0, false
	}
	colon := strings.Index(text[key:], ":")
	if colon < 0 {
		return 0, 0, false
	}
	open := strings.Index(text[key+colon:], `"`)
	if open < 0 {
		return 0, 0, false
	}
	start := key + colon + open + 1
	end := start
	for end < len(text) && !(text[end] == '"' && text[end-1] != '\\') {
		end++
	}

	var sum float64
	least = 1
	n := 0
	pos := 0
	for _, t := range tokens {
		from, to := pos, pos+len(t.Text)
		pos = to
		if to <= start || from >= end || !strings.ContainsFunc(t.Text, func(r rune) bool {
			return unicode.IsLetter(r) || unicode.IsDigit(r)
		}) {
			continue
		}
		sum += t.Logprob
		least = math.Min(least, math.Exp(t.Logprob))
		n++
	}
	if n == 0 {
		return 0, 0, false
	}
	return math.Exp(sum / float64(n)), least, true
}
