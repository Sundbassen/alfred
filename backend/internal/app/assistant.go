package app

import (
	"context"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
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
type Assistant struct {
	router      *assistant.Router
	recognizers []Recognizer
}

// NewAssistant builds an assistant over the router's intents with the
// given recognizers, tried in order.
func NewAssistant(r *assistant.Router, recognizers ...Recognizer) *Assistant {
	return &Assistant{router: r, recognizers: recognizers}
}

// Reply is the answer to one transcript.
type Reply struct {
	Text        string                // what to say
	Recognition assistant.Recognition // what was understood, and by which recognizer
}

// Respond recognizes the transcript's intent and runs its handler. It
// returns either a reply or an error:
//   - when no recognizer placed the transcript, the first recognizer's
//     error, typically a *match.NoMatchError (errors.Is fault.ErrNotFound)
//     with the templates' best near miss;
//   - otherwise the handler's error, wrapped with the intent name
//     (assistant.SpokenText says what to tell the user).
func (a *Assistant) Respond(ctx context.Context, transcript string) (*Reply, error) {
	rec, err := a.recognize(ctx, transcript)
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
	resp, err := a.router.Dispatch(ctx, intent)
	if err != nil {
		return nil, errors.WithMessage(err, rec.Intent)
	}
	return &Reply{Text: resp.Text, Recognition: rec}, nil
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
