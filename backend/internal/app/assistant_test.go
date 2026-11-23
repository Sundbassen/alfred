package app

import (
	"context"
	"errors"
	"testing"

	"github.com/sundbassen/alfred/internal/assistant"
)

type greet struct{}

func (greet) IntentName() string { return "greet" }

func newTestAssistant(err error) *Assistant {
	r := assistant.NewRouter()
	r.Use(assistant.SpeakErrors)
	assistant.Handle(r, assistant.Spec{Phrases: []string{"good morning"}},
		func(context.Context, greet) (assistant.Response, error) {
			return assistant.Response{Text: "Good morning!"}, err
		})
	return NewAssistant(r)
}

func TestRespond(t *testing.T) {
	a := newTestAssistant(nil)

	reply, err := a.Respond(context.Background(), "Good morning.")
	if err != nil || !reply.Matched || reply.Text != "Good morning!" || reply.Match.Name != "greet" {
		t.Errorf("Respond(good morning) = %+v, %v", reply, err)
	}

	reply, err = a.Respond(context.Background(), "tell me a joke")
	if err != nil || reply.Matched || reply.Text != NoMatchText {
		t.Errorf("Respond(joke) = %+v, %v; want no match", reply, err)
	}

	if got := a.Prompt(); got != "Good morning." {
		t.Errorf("Prompt() = %q", got)
	}
}

func TestRespondHandlerError(t *testing.T) {
	cause := errors.New("boom")
	reply, err := newTestAssistant(cause).Respond(context.Background(), "good morning")
	if !errors.Is(err, cause) || reply.Text != assistant.FailedText {
		t.Errorf("Respond = %+v, %v; want FailedText and the cause", reply, err)
	}
}
