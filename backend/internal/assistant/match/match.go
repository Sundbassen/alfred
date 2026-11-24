// Package match recognizes intents locally by matching transcripts against
// the example phrases of the registered intents. A phrase is either plain
// ("what time is it", matched exactly or by token overlap) or a template
// with slots ("play {query}"), matched literally with each slot capturing
// one or more words.
package match

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"gitlab.com/tozd/go/errors"
)

// DefaultThreshold is the minimum score for Recognize to report a match.
const DefaultThreshold = 0.75

// NoMatchError is returned by Recognize when no phrase scores at least the
// threshold. Best is the closest candidate (zero if there was none), for
// logging near misses. It is a fault.ErrNotFound (errors.Is).
type NoMatchError struct {
	Best assistant.Recognition
}

func (e *NoMatchError) Error() string {
	if e.Best.Intent == "" {
		return "no intent matched"
	}
	return fmt.Sprintf("no intent matched (best: %s %.2f)", e.Best.Intent, e.Best.Score)
}

func (e *NoMatchError) Unwrap() error { return fault.ErrNotFound }

// By is the Recognition.By of the template recognizer.
const By = "templates"

// Matcher recognizes intents by matching transcripts against their example
// phrases: normalization, then an exact match or token overlap (Dice).
type Matcher struct {
	// Threshold is the minimum score for Recognize to report a match.
	Threshold float64

	phrases []phrase
}

type phrase struct {
	intent     string
	text       string
	normalized string
	tokens     []string
	template   []element // non-nil when the phrase has slots
}

// element is one part of a template: literal words or a slot.
type element struct {
	words []string // literal, normalized; empty for a slot
	slot  string   // slot name; empty for literal words
}

var slotPattern = regexp.MustCompile(`\{(\w+)\}`)

// compileTemplate splits a phrase with slots into literal words and slots.
// It returns nil for a phrase without slots. Slots are found before
// normalization, which would strip the braces.
func compileTemplate(text string) []element {
	locs := slotPattern.FindAllStringSubmatchIndex(text, -1)
	if locs == nil {
		return nil
	}
	var elems []element
	literal := func(s string) {
		if words := strings.Fields(Normalize(s)); len(words) > 0 {
			elems = append(elems, element{words: words})
		}
	}
	prev := 0
	for _, loc := range locs {
		literal(text[prev:loc[0]])
		elems = append(elems, element{slot: text[loc[2]:loc[3]]})
		prev = loc[1]
	}
	literal(text[prev:])
	return elems
}

// literalWords counts a template's literal words: its specificity.
func literalWords(elems []element) int {
	n := 0
	for _, el := range elems {
		n += len(el.words)
	}
	return n
}

// matchTemplate matches the whole transcript (normalized tokens) against a
// template. Each slot captures at least one token; when a slot is followed
// by literal words it takes the longest capture, so "play {query} in the
// {speaker}" gives the query everything up to the last "in the".
func matchTemplate(elems []element, tokens []string) (map[string]string, bool) {
	slots := make(map[string]string)
	var match func(e, t int) bool
	match = func(e, t int) bool {
		if e == len(elems) {
			return t == len(tokens)
		}
		el := elems[e]
		if el.slot == "" {
			if t+len(el.words) > len(tokens) {
				return false
			}
			for i, w := range el.words {
				if tokens[t+i] != w {
					return false
				}
			}
			return match(e+1, t+len(el.words))
		}
		for end := len(tokens); end > t; end-- {
			if match(e+1, end) {
				slots[el.slot] = strings.Join(tokens[t:end], " ")
				return true
			}
		}
		return false
	}
	if !match(0, 0) {
		return nil, false
	}
	return slots, true
}

// New builds a matcher over the phrases of the given intents (from
// assistant.Router.Intents).
func New(routes []assistant.Route) *Matcher {
	m := &Matcher{Threshold: DefaultThreshold}
	for _, rt := range routes {
		for _, text := range rt.Spec.Phrases {
			normalized := Normalize(slotPattern.ReplaceAllString(text, ""))
			m.phrases = append(m.phrases, phrase{
				intent:     rt.Name,
				text:       text,
				normalized: normalized,
				tokens:     strings.Fields(normalized),
				template:   compileTemplate(text),
			})
		}
	}
	return m
}

// maxPromptWords caps Prompt so it stays well inside whisper's prompt
// window (half the 448-token text context) as commands are added.
const maxPromptWords = 150

// Prompt returns an initial prompt for speech-to-text that biases decoding
// toward the command phrases, written as separate sentences, e.g. "What
// time is it? Set a timer. Play." It is cut off at maxPromptWords, so every
// intent gets a fair share: phrases are taken round-robin across intents
// (each intent's first phrase, then each one's second, …), deduplicated.
// A template contributes its literal words before the first slot, without
// trailing function words ("set a timer for {when}" becomes "Set a
// timer."), and nothing if only question or function words remain ("what
// is on the {list}").
//
// Sentences, not a comma-separated list: whisper continues the style of its
// prompt, and after a list prompt greedy decoding looped ("what time is it,
// what is it, what is it, ...") until the temperature fallback rescued it
// at ~10x the latency.
func (m *Matcher) Prompt() string {
	// Candidate sentences per intent, in registration order.
	var order []string
	byIntent := make(map[string][]string)
	for _, p := range m.phrases {
		text := promptText(p)
		if text == "" {
			continue
		}
		if _, ok := byIntent[p.intent]; !ok {
			order = append(order, p.intent)
		}
		byIntent[p.intent] = append(byIntent[p.intent], text)
	}

	var sentences []string
	seen := make(map[string]bool)
	words := 0
	for round := 0; ; round++ {
		added := false
		for _, intent := range order {
			texts := byIntent[intent]
			if round >= len(texts) {
				continue
			}
			added = true
			text := texts[round]
			fields := strings.Fields(text)
			if seen[text] || words+len(fields) > maxPromptWords {
				continue
			}
			seen[text] = true
			words += len(fields)
			end := "."
			if questionWords[fields[0]] {
				end = "?"
			}
			sentences = append(sentences, strings.ToUpper(text[:1])+text[1:]+end)
		}
		if !added {
			break
		}
	}
	return strings.Join(sentences, " ")
}

// promptText is a phrase as it goes into the prompt (lowercase, without
// slots), or "" for a template fragment that would only add noise.
func promptText(p phrase) string {
	text := p.text
	if p.template != nil {
		text = text[:slotPattern.FindStringIndex(text)[0]]
	}
	fields := strings.Fields(strings.ToLower(text))
	if p.template != nil {
		for len(fields) > 0 && functionWords[fields[len(fields)-1]] {
			fields = fields[:len(fields)-1]
		}
		meaningful := false
		for _, f := range fields {
			if !functionWords[f] && !questionWords[f] {
				meaningful = true
			}
		}
		if !meaningful {
			return ""
		}
	}
	return strings.Join(fields, " ")
}

// functionWords are stripped from the end of a template's literal words.
var functionWords = map[string]bool{
	"the": true, "a": true, "an": true, "my": true, "your": true, "some": true, "any": true,
	"to": true, "for": true, "at": true, "in": true, "on": true, "of": true, "off": true,
	"from": true, "with": true, "by": true, "about": true, "called": true, "and": true,
}

var questionWords = map[string]bool{
	"what": true, "when": true, "where": true, "who": true, "why": true, "how": true,
	"is": true, "are": true, "can": true, "could": true, "do": true, "does": true, "will": true,
}

// Recognize returns the intent whose phrase best matches the transcript,
// or a *NoMatchError if no phrase scores at least the threshold. It is
// pure computation; ctx is for the recognizer interface.
func (m *Matcher) Recognize(_ context.Context, transcript string) (assistant.Recognition, error) {
	normalized := Normalize(transcript)
	tokens := strings.Fields(normalized)
	if len(tokens) == 0 {
		return assistant.Recognition{}, errors.WithStack(&NoMatchError{})
	}

	// Among full matches (score 1) the most specific wins, like
	// http.ServeMux: the one with the most literal words. "play {query} in
	// the {speaker}" beats "play {query}", and an exact plain phrase (all
	// literal) beats any template.
	var best assistant.Recognition
	bestLiterals := -1
	for _, p := range m.phrases {
		if p.template != nil {
			slots, ok := matchTemplate(p.template, tokens)
			if literals := literalWords(p.template); ok && literals > bestLiterals {
				best = assistant.Recognition{Intent: p.intent, Phrase: p.text, Score: 1, Slots: slots, By: By}
				bestLiterals = literals
			}
			continue
		}
		if p.normalized == normalized {
			if len(p.tokens) > bestLiterals {
				best = assistant.Recognition{Intent: p.intent, Phrase: p.text, Score: 1, By: By}
				bestLiterals = len(p.tokens)
			}
			continue
		}
		if score := dice(tokens, p.tokens); bestLiterals < 0 && score > best.Score {
			best = assistant.Recognition{Intent: p.intent, Phrase: p.text, Score: score, By: By}
		}
	}
	if best.Intent == "" || best.Score < m.Threshold {
		return assistant.Recognition{}, errors.WithStack(&NoMatchError{Best: best})
	}
	return best, nil
}

var contractions = map[string]string{
	"what's":  "what is",
	"it's":    "it is",
	"that's":  "that is",
	"there's": "there is",
	"what're": "what are",
	"i'm":     "i am",
	"don't":   "do not",
	"can't":   "cannot",
}

// ignored are filler and wake words that carry no command meaning.
var ignored = map[string]bool{
	"uh":     true,
	"um":     true,
	"umm":    true,
	"please": true,
	"hey":    true,
	"alfred": true,
}

// Normalize lowercases the text, expands common contractions, strips
// punctuation and drops filler and wake words, e.g. " What's the time,
// please?" becomes "what is the time".
func Normalize(text string) string {
	// Whisper may emit a typographic apostrophe.
	text = strings.ReplaceAll(strings.ToLower(text), "’", "'")

	var words []string
	for _, word := range strings.Fields(text) {
		word = strings.TrimFunc(word, func(r rune) bool {
			return r != '\'' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if expanded, ok := contractions[word]; ok {
			word = expanded
		}
		word = strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
				return r
			}
			if unicode.IsPunct(r) || unicode.IsSymbol(r) {
				return ' '
			}
			return r
		}, word)
		for _, w := range strings.Fields(word) {
			if !ignored[w] {
				words = append(words, w)
			}
		}
	}
	return strings.Join(words, " ")
}

// dice is the Sørensen–Dice coefficient of two token multisets:
// 2·|A∩B| / (|A|+|B|).
func dice(a, b []string) float64 {
	if len(a)+len(b) == 0 {
		return 0
	}
	counts := make(map[string]int, len(b))
	for _, t := range b {
		counts[t]++
	}
	common := 0
	for _, t := range a {
		if counts[t] > 0 {
			counts[t]--
			common++
		}
	}
	return 2 * float64(common) / float64(len(a)+len(b))
}
