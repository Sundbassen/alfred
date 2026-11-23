package app

import (
	"context"
	"testing"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"gitlab.com/tozd/go/errors"
)

type greet struct{}

func (greet) IntentName() string { return "greet" }

func newTestAssistant(err error) *Assistant {
	r := assistant.NewRouter()
	assistant.Handle(r, assistant.Spec{Phrases: []string{"good morning"}},
		func(context.Context, greet) (assistant.Response, error) {
			return assistant.Response{Text: "Good morning!"}, err
		})
	return NewAssistant(r)
}

func TestRespond(t *testing.T) {
	a := newTestAssistant(nil)

	reply, err := a.Respond(context.Background(), "Good morning.")
	if err != nil || reply.Text != "Good morning!" || reply.Match.Name != "greet" {
		t.Errorf("Respond(good morning) = %+v, %v", reply, err)
	}

	reply, err = a.Respond(context.Background(), "tell me a joke")
	var noMatch *match.NoMatchError
	if reply != nil || !errors.Is(err, fault.ErrNotFound) || !errors.As(err, &noMatch) {
		t.Errorf("Respond(joke) = %+v, %v; want nil and a *match.NoMatchError", reply, err)
	}

	if got := a.Prompt(); got != "Good morning." {
		t.Errorf("Prompt() = %q", got)
	}
}

func TestRespondHandlerError(t *testing.T) {
	cause := &assistant.SpokenError{Text: "Too early for that."}
	reply, err := newTestAssistant(cause).Respond(context.Background(), "good morning")
	if reply != nil || !errors.Is(err, cause) || assistant.SpokenText(err) != "Too early for that." {
		t.Errorf("Respond = %+v, %v; want nil and the wrapped SpokenError", reply, err)
	}
}

type play struct {
	Query string `slot:"query"`
}

func (play) IntentName() string { return "play" }

func TestRespondFillsSlots(t *testing.T) {
	r := assistant.NewRouter()
	assistant.Handle(r, assistant.Spec{Phrases: []string{"play {query}"}},
		func(_ context.Context, in play) (assistant.Response, error) {
			return assistant.Response{Text: "Playing " + in.Query + "."}, nil
		})
	reply, err := NewAssistant(r).Respond(context.Background(), "Hey Alfred, play Daft Punk.")
	if err != nil || reply.Text != "Playing daft punk." {
		t.Errorf("Respond = %+v, %v", reply, err)
	}
}
