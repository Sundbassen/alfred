package assistant

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type askTime struct{}

func (askTime) IntentName() string { return "time" }

type turnOn struct{ Room string }

func (turnOn) IntentName() string { return "lights_on" }

func TestDispatchPassesConcreteType(t *testing.T) {
	r := NewRouter()
	Handle(r, Spec{Description: "time"}, func(ctx context.Context, _ askTime) (Response, error) {
		return Response{Text: "noon"}, nil
	})
	Handle(r, Spec{Description: "lights"}, func(ctx context.Context, in turnOn) (Response, error) {
		return Response{Text: "on in " + in.Room}, nil
	})

	resp, err := r.Dispatch(context.Background(), turnOn{Room: "kitchen"})
	if err != nil || resp.Text != "on in kitchen" {
		t.Errorf("Dispatch(turnOn) = %q, %v", resp.Text, err)
	}
	resp, err = r.Dispatch(context.Background(), askTime{})
	if err != nil || resp.Text != "noon" {
		t.Errorf("Dispatch(askTime) = %q, %v", resp.Text, err)
	}
}

func TestIntentsAndNew(t *testing.T) {
	r := NewRouter()
	Handle(r, Spec{Phrases: []string{"what time is it"}}, func(context.Context, askTime) (Response, error) {
		return Response{}, nil
	})
	Handle(r, Spec{}, func(context.Context, turnOn) (Response, error) { return Response{}, nil })

	routes := r.Intents()
	if len(routes) != 2 || routes[0].Name != "time" || routes[1].Name != "lights_on" ||
		routes[0].Spec.Phrases[0] != "what time is it" {
		t.Errorf("Intents() = %+v", routes)
	}

	in, err := r.New("lights_on")
	if _, ok := in.(turnOn); !ok || err != nil {
		t.Errorf("New(lights_on) = %T, %v; want turnOn", in, err)
	}
	if _, err := r.New("weather"); !errors.Is(err, ErrUnknownIntent) {
		t.Errorf("New(weather) error = %v, want ErrUnknownIntent", err)
	}
}

func TestDispatchUnknownIntent(t *testing.T) {
	_, err := NewRouter().Dispatch(context.Background(), askTime{})
	if !errors.Is(err, ErrUnknownIntent) {
		t.Errorf("error = %v, want ErrUnknownIntent", err)
	}
}

func TestHandlePanicsOnDuplicate(t *testing.T) {
	r := NewRouter()
	h := func(context.Context, askTime) (Response, error) { return Response{}, nil }
	Handle(r, Spec{}, h)
	defer func() {
		if recover() == nil {
			t.Error("registering an intent twice did not panic")
		}
	}()
	Handle(r, Spec{}, h)
}

func TestMiddlewareOrder(t *testing.T) {
	r := NewRouter()
	var calls []string
	mw := func(name string) Middleware {
		return func(next Handler) Handler {
			return HandlerFunc(func(ctx context.Context, in Intent) (Response, error) {
				calls = append(calls, name)
				return next.Handle(ctx, in)
			})
		}
	}
	r.Use(mw("outer"), mw("inner"))
	Handle(r, Spec{}, func(context.Context, askTime) (Response, error) {
		calls = append(calls, "handler")
		return Response{}, nil
	})

	if _, err := r.Dispatch(context.Background(), askTime{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "outer,inner,handler" {
		t.Errorf("call order = %s", got)
	}
}

func TestSpeakErrors(t *testing.T) {
	cause := errors.New("connection refused")
	tests := []struct {
		err      error
		wantText string
	}{
		{&SpokenError{Text: "I can't reach the lights", Err: cause}, "I can't reach the lights"},
		{cause, FailedText},
	}
	for _, tt := range tests {
		r := NewRouter()
		r.Use(SpeakErrors)
		Handle(r, Spec{}, func(context.Context, askTime) (Response, error) { return Response{}, tt.err })

		resp, err := r.Dispatch(context.Background(), askTime{})
		if resp.Text != tt.wantText {
			t.Errorf("Text = %q, want %q", resp.Text, tt.wantText)
		}
		if !errors.Is(err, cause) {
			t.Errorf("error = %v, want it to wrap the cause", err)
		}
	}
}
