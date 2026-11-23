// Package assistant routes intents (what the user wants) to the handlers
// that carry them out. Recognizers (assistant/match today, an LLM fallback
// later) decide which intent was meant; the Router dispatches it to the
// handler registered for its concrete type. The handlers themselves live in
// internal/commands and get their services injected at registration.
package assistant

import (
	"context"

	"gitlab.com/tozd/go/errors"
)

// Intent is implemented by every concrete intent type, e.g.
//
//	type AskTime struct{}
//	func (AskTime) IntentName() string { return "time" }
//
// Intent types should be non-pointer structs with a value receiver, so
// their zero value can name itself. Parameters ("slots") are fields of the
// type.
type Intent interface {
	IntentName() string
}

// Response is what a handler says back. It is a plain struct so it can be
// serialized as is (e.g. by a future HTTP API).
type Response struct {
	Text string `json:"text"`
}

// Spec describes an intent to recognizers.
type Spec struct {
	// Description is a one-line summary of what the intent does (for an LLM
	// recognizer later, and for listings).
	Description string
	// Phrases are example phrasings, used by the local matcher and to bias
	// speech-to-text toward them.
	Phrases []string
}

// Handler carries out an intent. Handlers registered with Handle receive
// their concrete intent type; this type-erased form is what middleware
// wraps.
type Handler interface {
	Handle(ctx context.Context, intent Intent) (Response, error)
}

// HandlerFunc adapts a function to a Handler, like http.HandlerFunc.
type HandlerFunc func(ctx context.Context, intent Intent) (Response, error)

func (f HandlerFunc) Handle(ctx context.Context, intent Intent) (Response, error) {
	return f(ctx, intent)
}

// Middleware wraps a Handler, e.g. for logging or error handling.
type Middleware func(Handler) Handler

// ErrUnknownIntent is returned by Router.Dispatch and Router.New for an
// intent no handler is registered for.
var ErrUnknownIntent = errors.Base("unknown intent")

// SpokenError is a failure the user should hear about, e.g. "I couldn't
// find a room called attic". Handlers return it, wrapping the cause;
// SpokenText extracts what to say.
type SpokenError struct {
	Text string // what to say
	Err  error  // the cause, for logs; may be nil
}

func (e *SpokenError) Error() string {
	if e.Err == nil {
		return e.Text
	}
	return e.Text + ": " + e.Err.Error()
}

func (e *SpokenError) Unwrap() error { return e.Err }
