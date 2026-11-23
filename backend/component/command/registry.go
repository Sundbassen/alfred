package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// DefaultThreshold is the minimum match score for a command to be executed.
const DefaultThreshold = 0.75

// ErrNoMatch is returned by Handle when no command matches the transcript
// confidently. This is where the cloud LLM fallback will plug in.
var ErrNoMatch = errors.New("no command matched")

// Match is the best command match for a transcript.
type Match struct {
	Command Command // nil when the registry is empty or the transcript has no words
	Phrase  string  // the phrase that matched best
	Score   float64 // 0..1; 1 is an exact match after normalization
}

// Registry holds the known commands and matches transcripts against their
// phrases.
type Registry struct {
	// Threshold is the minimum score for Match to report a match.
	Threshold float64

	commands []Command
	phrases  []phrase
}

type phrase struct {
	command    Command
	text       string
	normalized string
	tokens     []string
}

// NewRegistry builds a registry of the given commands. It panics on a
// duplicate command ID, which is a programming error.
func NewRegistry(cmds ...Command) *Registry {
	r := &Registry{Threshold: DefaultThreshold}
	seen := make(map[string]bool)
	for _, cmd := range cmds {
		if seen[cmd.ID()] {
			panic(fmt.Sprintf("command: duplicate command ID %q", cmd.ID()))
		}
		seen[cmd.ID()] = true
		r.commands = append(r.commands, cmd)
		for _, text := range cmd.Phrases() {
			normalized := normalize(text)
			r.phrases = append(r.phrases, phrase{
				command:    cmd,
				text:       text,
				normalized: normalized,
				tokens:     strings.Fields(normalized),
			})
		}
	}
	return r
}

// Commands returns the registered commands in registration order.
func (r *Registry) Commands() []Command {
	return r.commands
}

// Match returns the best matching command phrase for the transcript. ok
// reports whether its score reaches the threshold; the best candidate is
// returned either way, for logging near misses.
func (r *Registry) Match(transcript string) (best Match, ok bool) {
	normalized := normalize(transcript)
	tokens := strings.Fields(normalized)
	if len(tokens) == 0 {
		return Match{}, false
	}

	for _, p := range r.phrases {
		var score float64
		if p.normalized == normalized {
			score = 1
		} else {
			score = dice(tokens, p.tokens)
		}
		if score > best.Score {
			best = Match{Command: p.command, Phrase: p.text, Score: score}
		}
	}
	return best, best.Command != nil && best.Score >= r.Threshold
}

// Handle matches the transcript and executes the matched command. It
// returns ErrNoMatch (with the best near miss) when nothing matches.
func (r *Registry) Handle(ctx context.Context, transcript string) (Response, Match, error) {
	match, ok := r.Match(transcript)
	if !ok {
		return Response{}, match, ErrNoMatch
	}
	resp, err := match.Command.Execute(ctx, Request{Transcript: transcript})
	if err != nil {
		return Response{}, match, fmt.Errorf("execute %s: %w", match.Command.ID(), err)
	}
	return resp, match, nil
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
