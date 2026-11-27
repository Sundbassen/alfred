package app

import (
	"context"
	"testing"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

type greet struct{}

func (greet) IntentName() string { return "greet" }

type play struct {
	Query string `slot:"query"`
}

func (play) IntentName() string { return "play" }

func newTestRouter(err error) *assistant.Router {
	r := assistant.NewRouter()
	assistant.Handle(r, assistant.Spec{Phrases: []string{"good morning"}},
		func(context.Context, greet) (assistant.Response, error) {
			return assistant.Response{Text: "Good morning!"}, err
		})
	assistant.Handle(r, assistant.Spec{Phrases: []string{"play {query}"}},
		func(_ context.Context, in play) (assistant.Response, error) {
			return assistant.Response{Text: "Playing " + in.Query + "."}, nil
		})
	return r
}

// withTemplates is the usual first tier.
func withTemplates(r *assistant.Router, more ...Recognizer) *Assistant {
	return NewAssistant(r, append([]Recognizer{match.New(r.Intents())}, more...)...)
}

func TestRespond(t *testing.T) {
	a := withTemplates(newTestRouter(nil))

	reply, err := a.Respond(context.Background(), "Good morning.")
	if err != nil || reply.Text != "Good morning!" || reply.Recognition.Intent != "greet" || reply.Recognition.By != match.By {
		t.Errorf("Respond(good morning) = %+v, %v", reply, err)
	}

	reply, err = a.Respond(context.Background(), "tell me a joke")
	var noMatch *match.NoMatchError
	if reply != nil || !errors.Is(err, fault.ErrNotFound) || !errors.As(err, &noMatch) {
		t.Errorf("Respond(joke) = %+v, %v; want nil and a *match.NoMatchError", reply, err)
	}
}

func TestRespondFillsSlots(t *testing.T) {
	reply, err := withTemplates(newTestRouter(nil)).Respond(context.Background(), "Hey Alfred, play Daft Punk.")
	if err != nil || reply.Text != "Playing daft punk." {
		t.Errorf("Respond = %+v, %v", reply, err)
	}
}

func TestRespondHandlerError(t *testing.T) {
	cause := &assistant.SpokenError{Text: "Too early for that."}
	reply, err := withTemplates(newTestRouter(cause)).Respond(context.Background(), "good morning")
	if reply != nil || !errors.Is(err, cause) || assistant.SpokenText(err) != "Too early for that." {
		t.Errorf("Respond = %+v, %v; want nil and the wrapped SpokenError", reply, err)
	}
}

// fakeRecognizer returns a scripted recognition or error and counts calls.
type fakeRecognizer struct {
	rec   assistant.Recognition
	err   error
	calls int
}

func (f *fakeRecognizer) Recognize(context.Context, string) (assistant.Recognition, error) {
	f.calls++
	return f.rec, f.err
}

func TestTiers(t *testing.T) {
	model := assistant.Recognition{Intent: "play", Slots: map[string]string{"query": "queen"}, By: "model"}

	t.Run("templates hit, model not asked", func(t *testing.T) {
		second := &fakeRecognizer{rec: model}
		reply, err := withTemplates(newTestRouter(nil), second).Respond(context.Background(), "good morning")
		if err != nil || reply.Recognition.By != match.By || second.calls != 0 {
			t.Errorf("reply %+v, err %v, model calls %d", reply, err, second.calls)
		}
	})

	t.Run("templates miss, model answers", func(t *testing.T) {
		second := &fakeRecognizer{rec: model}
		reply, err := withTemplates(newTestRouter(nil), second).Respond(context.Background(), "could you throw on some queen")
		if err != nil || reply.Text != "Playing queen." || reply.Recognition.By != "model" {
			t.Errorf("reply %+v, err %v", reply, err)
		}
	})

	for name, modelErr := range map[string]error{
		"model has no match": errors.WithMessage(fault.ErrNotFound, "none"),
		"model unavailable":  errors.WithMessage(fault.ErrServiceUnavailable, "loading"),
		"model too slow":     errors.WithMessage(fault.ErrTimeout, "3s"),
	} {
		t.Run(name+": templates' near miss is returned", func(t *testing.T) {
			second := &fakeRecognizer{err: modelErr}
			_, err := withTemplates(newTestRouter(nil), second).Respond(context.Background(), "tell me a joke")
			var noMatch *match.NoMatchError
			if !errors.As(err, &noMatch) || second.calls != 1 {
				t.Errorf("err %v, model calls %d; want the templates' NoMatchError", err, second.calls)
			}
		})
	}

	t.Run("other errors stop", func(t *testing.T) {
		boom := errors.WithMessage(fault.ErrInternal, "boom")
		first := &fakeRecognizer{err: boom}
		second := &fakeRecognizer{rec: model}
		_, err := NewAssistant(newTestRouter(nil), first, second).Respond(context.Background(), "x")
		if !errors.Is(err, fault.ErrInternal) || second.calls != 0 {
			t.Errorf("err %v, second calls %d; want ErrInternal and no second call", err, second.calls)
		}
	})
}

func TestRouteSlots(t *testing.T) {
	for _, rt := range newTestRouter(nil).Intents() {
		want := map[string]int{"greet": 0, "play": 1}[rt.Name]
		if len(rt.Slots) != want || (want == 1 && rt.Slots[0] != (assistant.Slot{Name: "query"})) {
			t.Errorf("%s slots = %v", rt.Name, rt.Slots)
		}
	}
}

type question struct{ Text string }

func (question) IntentName() string { return "question" }

func TestFallback(t *testing.T) {
	r := newTestRouter(nil)
	var asked []string
	assistant.Handle(r, assistant.Spec{}, func(_ context.Context, q question) (assistant.Response, error) {
		asked = append(asked, q.Text)
		return assistant.Response{Text: "I don't know."}, nil
	})
	a := withTemplates(r)
	a.SetFallback(func(transcript string) assistant.Intent { return question{Text: transcript} })

	reply, err := a.Respond(context.Background(), "Who wrote Hamlet?")
	if err != nil || reply.Text != "I don't know." || reply.Recognition.By != FallbackBy || len(asked) != 1 {
		t.Errorf("fallback: %+v, %v, asked %q", reply, err, asked)
	}
	if reply, err := a.Respond(context.Background(), "Good morning."); err != nil || reply.Recognition.Intent != "greet" {
		t.Errorf("a command still goes to its intent: %+v, %v", reply, err)
	}
	if _, err := a.Respond(context.Background(), "Uh, thanks."); !errors.Is(err, fault.ErrNotFound) || len(asked) != 1 {
		t.Errorf("short noise: %v, asked %q; want a no-match and no question", err, asked)
	}
}

func TestFallbackUnavailable(t *testing.T) {
	// While the model loads (or is down), a miss is still a miss: the
	// no-match error, not "can't reach that".
	for _, cause := range []error{fault.ErrServiceUnavailable, fault.ErrTimeout} {
		r := newTestRouter(nil)
		assistant.Handle(r, assistant.Spec{}, func(context.Context, question) (assistant.Response, error) {
			return assistant.Response{}, errors.WithMessage(cause, "model")
		})
		a := withTemplates(r)
		a.SetFallback(func(transcript string) assistant.Intent { return question{Text: transcript} })
		_, err := a.Respond(context.Background(), "Who wrote Hamlet?")
		var noMatch *match.NoMatchError
		if !errors.As(err, &noMatch) {
			t.Errorf("%v from the fallback: %v, want the templates' no-match error", cause, err)
		}
	}
}
