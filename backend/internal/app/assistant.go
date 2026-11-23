package app

import (
	"context"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"gitlab.com/tozd/go/errors"
)

// NoMatchText is what to say when no intent matches the transcript.
const NoMatchText = "Sorry, I didn't catch that."

// Assistant answers transcripts: it recognizes the intent and dispatches it
// to its handler.
type Assistant struct {
	router  *assistant.Router
	matcher *match.Matcher
}

// NewAssistant builds an assistant over the router's intents.
func NewAssistant(r *assistant.Router) *Assistant {
	return &Assistant{router: r, matcher: match.New(r.Intents())}
}

// Prompt biases speech-to-text toward the registered phrases.
func (a *Assistant) Prompt() string {
	return a.matcher.Prompt()
}

// Reply is the answer to one transcript.
type Reply struct {
	Text  string       // what to say
	Match match.Result // the recognized intent
}

// Respond recognizes the transcript's intent and runs its handler. It
// returns either a reply or an error: a *match.NoMatchError (errors.Is
// fault.ErrNotFound) when nothing matched (the cloud LLM fallback, #2,
// plugs in here), or the handler's error, wrapped with the intent name
// (assistant.SpokenText says what to tell the user).
func (a *Assistant) Respond(ctx context.Context, transcript string) (*Reply, error) {
	res, err := a.matcher.Recognize(transcript)
	if err != nil {
		return nil, err
	}

	intent, err := a.router.New(res.Name)
	if err != nil {
		return nil, err
	}
	intent, err = assistant.Fill(intent, res.Slots)
	if err != nil {
		return nil, err
	}
	resp, err := a.router.Dispatch(ctx, intent)
	if err != nil {
		return nil, errors.WithMessage(err, res.Name)
	}
	return &Reply{Text: resp.Text, Match: res}, nil
}
