package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

// NoMatchText is what to say when no intent matches the transcript.
const NoMatchText = "Sorry, I didn't catch that."

// Recognizer understands a transcript as an intent. A recognizer that
// can't place the transcript returns a fault.ErrNotFound (e.g. a
// *match.NoMatchError); one that can't answer right now returns
// fault.ErrServiceUnavailable or fault.ErrTimeout. Both hand the
// transcript to the next recognizer.
type Recognizer interface {
	Recognize(ctx context.Context, transcript string) (assistant.Recognition, error)
}

// Assistant answers transcripts: it recognizes the intent, trying its
// recognizers in order (cheapest first, e.g. phrase templates, then a
// local model), and dispatches the first recognition to its handler.
//
// When a handler asks a follow-up question (assistant.Response.Ask), the
// next transcript within QuestionTTL answers it, unless the first
// recognizer matches it fully (Score 1) as a command of its own. So the
// first recognizer should be the phrase templates: anything else they
// don't match exactly (a fuzzy match, something only a model places) is
// taken as the answer. One Assistant holds one pending question: it serves
// one conversation at a time.
type Assistant struct {
	router      *assistant.Router
	recognizers []Recognizer
	now         func() time.Time

	mu       sync.Mutex
	question *assistant.Question // pending follow-up question
	askedAt  time.Time

	fallback func(transcript string) assistant.Intent
}

// FallbackBy is the Recognition.By of a transcript handed to the fallback.
const FallbackBy = "fallback"

// minFallbackWords keeps short noise ("uh", "thanks", a false wake) away
// from the fallback.
const minFallbackWords = 3

// SetFallback makes transcripts no recognizer places go to the intent
// fallback returns for them (e.g. a question for a language model),
// instead of returning a no-match error. Transcripts of fewer than three
// words still return the error, and so does a fallback that can't answer
// right now (fault.ErrServiceUnavailable or fault.ErrTimeout).
func (a *Assistant) SetFallback(fallback func(transcript string) assistant.Intent) {
	a.fallback = fallback
}

// QuestionTTL is how long a follow-up question waits for its answer,
// counted from when its handler returned, or from Listening if the front
// end calls it once it has said the question.
const QuestionTTL = 15 * time.Second

// FollowUpBy is the Recognition.By of an answer to a follow-up question.
const FollowUpBy = "follow-up"

// NewAssistant builds an assistant over the router's intents with the
// given recognizers, tried in order. The first one also decides whether a
// transcript is a command or the answer to a pending question (see
// Assistant), so pass the phrase templates first.
func NewAssistant(r *assistant.Router, recognizers ...Recognizer) *Assistant {
	return &Assistant{router: r, recognizers: recognizers, now: time.Now}
}

// Reply is the answer to one transcript.
type Reply struct {
	Text        string                // what to say
	Recognition assistant.Recognition // what was understood, and by which recognizer
	// Listen is set when Text asks a question: the front end should listen
	// for the answer without the wake word.
	Listen bool
}

// Respond recognizes the transcript's intent and runs its handler. It
// returns either a reply or an error:
//   - when no recognizer placed the transcript, the first recognizer's
//     error, typically a *match.NoMatchError (errors.Is fault.ErrNotFound)
//     with the templates' best near miss;
//   - otherwise the handler's error, wrapped with the intent name
//     (assistant.SpokenText says what to tell the user).
func (a *Assistant) Respond(ctx context.Context, transcript string) (*Reply, error) {
	var command *assistant.Recognition
	if q := a.takeQuestion(); q != nil {
		// A full match by the first recognizer is a command of its own.
		if len(a.recognizers) > 0 {
			if rec, err := a.recognizers[0].Recognize(ctx, transcript); err == nil && rec.Score >= 1 {
				command = &rec
			}
		}
		if command == nil {
			reply, answered, err := a.answer(ctx, *q, transcript)
			if answered {
				return reply, err
			}
		}
	}

	var rec assistant.Recognition
	var err error
	if command != nil {
		rec = *command
	} else {
		rec, err = a.recognize(ctx, transcript)
	}
	if errors.Is(err, fault.ErrNotFound) && a.fallback != nil &&
		len(strings.Fields(match.Normalize(transcript))) >= minFallbackWords {
		intent := a.fallback(transcript)
		reply, ferr := a.dispatch(ctx, intent, assistant.Recognition{Intent: intent.IntentName(), Phrase: transcript, Score: 1, By: FallbackBy})
		if errors.Is(ferr, fault.ErrServiceUnavailable) || errors.Is(ferr, fault.ErrTimeout) {
			// The fallback can't answer right now (the model is loading or
			// down): this is still a transcript nothing understood.
			return nil, err
		}
		return reply, ferr
	}
	if err != nil {
		return nil, err
	}

	intent, err := a.router.New(rec.Intent)
	if err != nil {
		return nil, err
	}
	intent, err = assistant.Fill(intent, rec.Slots)
	if err != nil {
		return nil, err
	}
	return a.dispatch(ctx, intent, rec)
}

// dispatch runs the intent's handler and keeps a follow-up question it asks.
func (a *Assistant) dispatch(ctx context.Context, intent assistant.Intent, rec assistant.Recognition) (*Reply, error) {
	resp, err := a.router.Dispatch(ctx, intent)
	if err != nil {
		return nil, errors.WithMessage(err, rec.Intent)
	}
	reply := &Reply{Text: resp.Text, Recognition: rec}
	if resp.Ask != nil && resp.Ask.Intent != nil {
		a.mu.Lock()
		a.question, a.askedAt = resp.Ask, a.now()
		a.mu.Unlock()
		reply.Listen = true
	}
	return reply, nil
}

// Listening restarts the pending question's QuestionTTL: the front end
// calls it when it starts listening for the answer, after saying the
// question, so a long question doesn't use up the time to answer it.
func (a *Assistant) Listening() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.question != nil {
		a.askedAt = a.now()
	}
}

// takeQuestion returns and clears the pending question, if it hasn't
// expired. Any transcript clears it: it is answered once or not at all.
func (a *Assistant) takeQuestion() *assistant.Question {
	a.mu.Lock()
	defer a.mu.Unlock()
	q := a.question
	a.question = nil
	if q == nil || a.now().Sub(a.askedAt) > QuestionTTL {
		return nil
	}
	return q
}

// answer treats the transcript as the answer to q. It reports answered =
// false when the transcript is not an answer (anything but yes or no to a
// yes/no question); Respond then handles it as usual.
//
// To a yes/no question, an answer starting with no ("no, thanks") is no. To
// a slot question only a plain no is ("no", "not now"); "no, the one hour
// one" corrects the answer, and the rest fills the slot.
func (a *Assistant) answer(ctx context.Context, q assistant.Question, transcript string) (*Reply, bool, error) {
	words := match.Normalize(transcript)
	rec := assistant.Recognition{Intent: q.Intent.IntentName(), Phrase: transcript, Score: 1, By: FollowUpBy}
	no, rest := isNo(words)
	if no && (q.Slot == "" || rest == "") {
		return &Reply{Text: "OK.", Recognition: rec}, true, nil
	}
	intent := q.Intent
	if q.Slot == "" {
		if !isYes(words) {
			return nil, false, nil
		}
	} else {
		if no {
			words = rest
		}
		var err error
		if intent, err = assistant.SetSlot(intent, q.Slot, words); err != nil {
			return nil, true, errors.WithMessage(err, rec.Intent)
		}
		rec.Slots = map[string]string{q.Slot: words}
	}
	reply, err := a.dispatch(ctx, intent, rec)
	return reply, true, err
}

var (
	yesWords = map[string]bool{"yes": true, "yeah": true, "yep": true, "yup": true, "sure": true,
		"ok": true, "okay": true, "correct": true, "right": true, "absolutely": true}
	noWords = map[string]bool{"no": true, "nope": true, "nah": true}
)

func isYes(words string) bool {
	fields := strings.Fields(words)
	return len(fields) > 0 && (yesWords[fields[0]] || words == "do it" || words == "go ahead")
}

// isNo reports whether words start with a no, and the words after it.
func isNo(words string) (no bool, rest string) {
	if words == "do not" || words == "not now" {
		return true, ""
	}
	first, rest, _ := strings.Cut(words, " ")
	return noWords[first], rest
}

// recognize returns the first recognition. A miss, an unavailable or a
// slow recognizer falls through to the next one; any other error stops.
func (a *Assistant) recognize(ctx context.Context, transcript string) (assistant.Recognition, error) {
	var first error
	for _, r := range a.recognizers {
		rec, err := r.Recognize(ctx, transcript)
		if err == nil {
			return rec, nil
		}
		if !fallsThrough(err) {
			return assistant.Recognition{}, err
		}
		if first == nil {
			first = err
		}
	}
	if first == nil {
		return assistant.Recognition{}, errors.WithMessage(fault.ErrNotFound, "no recognizers")
	}
	return assistant.Recognition{}, first
}

func fallsThrough(err error) bool {
	return errors.Is(err, fault.ErrNotFound) ||
		errors.Is(err, fault.ErrServiceUnavailable) ||
		errors.Is(err, fault.ErrTimeout)
}
