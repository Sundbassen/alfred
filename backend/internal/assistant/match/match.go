// Package match recognizes intents locally by matching transcripts against
// the example phrases of the registered intents.
package match

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/sundbassen/alfred/internal/assistant"
)

// DefaultThreshold is the minimum score for Recognize to report a match.
const DefaultThreshold = 0.75

// ErrNoMatch is matched (errors.Is) by every *NoMatchError.
var ErrNoMatch = errors.New("no intent matched")

// NoMatchError is returned by Recognize when no phrase scores at least the
// threshold. Best is the closest candidate (zero if there was none), for
// logging near misses.
type NoMatchError struct {
	Best Result
}

func (e *NoMatchError) Error() string {
	if e.Best.Name == "" {
		return ErrNoMatch.Error()
	}
	return fmt.Sprintf("%s (best: %s %.2f)", ErrNoMatch, e.Best.Name, e.Best.Score)
}

func (e *NoMatchError) Is(target error) bool { return target == ErrNoMatch }

// Result is the best phrase match for a transcript.
type Result struct {
	Name   string  // intent name; empty when there are no phrases or the transcript has no words
	Phrase string  // the phrase that matched best
	Score  float64 // 0..1; 1 is an exact match after normalization
}

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
}

// New builds a matcher over the phrases of the given intents (from
// assistant.Router.Intents).
func New(routes []assistant.Route) *Matcher {
	m := &Matcher{Threshold: DefaultThreshold}
	for _, rt := range routes {
		for _, text := range rt.Spec.Phrases {
			normalized := normalize(text)
			m.phrases = append(m.phrases, phrase{
				intent:     rt.Name,
				text:       text,
				normalized: normalized,
				tokens:     strings.Fields(normalized),
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
// time is it? Tell me the time." Phrases are deduplicated, kept in
// registration order, and cut off at maxPromptWords.
//
// Sentences, not a comma-separated list: whisper continues the style of its
// prompt, and after a list prompt greedy decoding looped ("what time is it,
// what is it, what is it, ...") until the temperature fallback rescued it
// at ~10x the latency.
func (m *Matcher) Prompt() string {
	var sentences []string
	seen := make(map[string]bool)
	words := 0
	for _, p := range m.phrases {
		text := strings.TrimSpace(strings.ToLower(p.text))
		if text == "" || seen[text] {
			continue
		}
		fields := strings.Fields(text)
		if words+len(fields) > maxPromptWords {
			break
		}
		seen[text] = true
		words += len(fields)

		end := "."
		if questionWords[fields[0]] {
			end = "?"
		}
		sentences = append(sentences, strings.ToUpper(text[:1])+text[1:]+end)
	}
	return strings.Join(sentences, " ")
}

var questionWords = map[string]bool{
	"what": true, "when": true, "where": true, "who": true, "why": true, "how": true,
	"is": true, "are": true, "can": true, "could": true, "do": true, "does": true, "will": true,
}

// Recognize returns the intent whose phrase best matches the transcript,
// or a *NoMatchError if no phrase scores at least the threshold.
func (m *Matcher) Recognize(transcript string) (Result, error) {
	normalized := normalize(transcript)
	tokens := strings.Fields(normalized)
	if len(tokens) == 0 {
		return Result{}, &NoMatchError{}
	}

	var best Result
	for _, p := range m.phrases {
		var score float64
		if p.normalized == normalized {
			score = 1
		} else {
			score = dice(tokens, p.tokens)
		}
		if score > best.Score {
			best = Result{Name: p.intent, Phrase: p.text, Score: score}
		}
	}
	if best.Name == "" || best.Score < m.Threshold {
		return Result{}, &NoMatchError{Best: best}
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

// normalize lowercases the text, expands common contractions, strips
// punctuation and drops filler and wake words, e.g. " What's the time,
// please?" becomes "what is the time".
func normalize(text string) string {
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
