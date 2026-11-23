package app

import (
	"context"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
)

// NoMatchText is said when no intent matches the transcript.
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
	Text    string       // what to say; set whenever there is something to say, even with an error
	Matched bool         // false: no intent matched (Text is NoMatchText)
	Match   match.Result // the recognized intent, or the best near miss when !Matched
}

// Respond recognizes the transcript's intent and runs its handler. With no
// match, the reply is NoMatchText (the cloud LLM fallback, #2, plugs in
// here). A handler error is returned along with the reply to speak.
func (a *Assistant) Respond(ctx context.Context, transcript string) (Reply, error) {
	res, ok := a.matcher.Recognize(transcript)
	if !ok {
		return Reply{Text: NoMatchText, Match: res}, nil
	}

	intent, err := a.router.New(res.Name)
	if err != nil {
		return Reply{Match: res}, err
	}
	resp, err := a.router.Dispatch(ctx, intent)
	return Reply{Text: resp.Text, Matched: true, Match: res}, err
}
