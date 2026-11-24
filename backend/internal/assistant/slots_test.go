package assistant

import (
	"context"
	"slices"
	"testing"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

type play struct {
	Query string `slot:"query"`
	Other int    // untagged fields are left alone
}

func (play) IntentName() string { return "play" }

type remind struct {
	When    string `slot:"when"`
	Message string `slot:"message,optional"`
}

func (remind) IntentName() string { return "remind" }

type badTag struct {
	When string `slot:"when,maybe"`
}

func (badTag) IntentName() string { return "bad_tag" }

type badSlot struct {
	Volume int `slot:"volume"`
}

func (badSlot) IntentName() string { return "bad" }

func TestFill(t *testing.T) {
	got, err := Fill(play{Other: 7}, map[string]string{"query": "daft punk"})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := got.(play); !ok || p.Query != "daft punk" || p.Other != 7 {
		t.Errorf("Fill = %#v", got)
	}

	if got, err := Fill(askTime{}, nil); err != nil || got != (askTime{}) {
		t.Errorf("Fill(no slots) = %#v, %v", got, err)
	}
}

func TestFillOptional(t *testing.T) {
	got, err := Fill(remind{}, map[string]string{"when": "10 minutes"})
	if err != nil || got != (remind{When: "10 minutes"}) {
		t.Errorf("Fill(optional missing) = %#v, %v", got, err)
	}
	got, err = Fill(remind{}, map[string]string{"when": "10 minutes", "message": "call mom"})
	if err != nil || got != (remind{When: "10 minutes", Message: "call mom"}) {
		t.Errorf("Fill(optional given) = %#v, %v", got, err)
	}
	if _, err := Fill(remind{}, map[string]string{"message": "call mom"}); !errors.Is(err, fault.ErrInvalidValue) {
		t.Errorf("Fill(required missing): error = %v, want fault.ErrInvalidValue", err)
	}
}

func TestRouteSlots(t *testing.T) {
	r := NewRouter()
	Handle(r, Spec{Phrases: []string{"remind me to {message} in {when}", "set a timer for {when}"}},
		func(context.Context, remind) (Response, error) { return Response{}, nil })
	want := []Slot{{Name: "message", Optional: true}, {Name: "when"}}
	if got := r.Intents()[0].Slots; !slices.Equal(got, want) {
		t.Errorf("Slots = %+v, want %+v", got, want)
	}
}

func TestFillErrors(t *testing.T) {
	tests := []struct {
		name   string
		intent Intent
		slots  map[string]string
		want   error
	}{
		{"missing slot", play{}, nil, fault.ErrInvalidValue},
		{"unknown slot", play{}, map[string]string{"query": "x", "room": "kitchen"}, fault.ErrInvalidValue},
		{"slot on intent without fields", askTime{}, map[string]string{"query": "x"}, fault.ErrInvalidValue},
		{"non-string slot field", badSlot{}, map[string]string{"volume": "3"}, fault.ErrUnexpectedType},
		{"unknown tag option", badTag{}, map[string]string{"when": "now"}, fault.ErrInvalidValue},
	}
	for _, tt := range tests {
		if _, err := Fill(tt.intent, tt.slots); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestHandleChecksPhraseSlots(t *testing.T) {
	h := func(context.Context, play) (Response, error) { return Response{}, nil }

	// A phrase slot with a matching field registers fine.
	Handle(NewRouter(), Spec{Phrases: []string{"play {query}"}}, h)

	for name, spec := range map[string]Spec{
		"unknown slot":     {Phrases: []string{"play {song}"}},
		"non-string field": {},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Handle did not panic", name)
				}
			}()
			if name == "non-string field" {
				Handle(NewRouter(), spec, func(context.Context, badSlot) (Response, error) { return Response{}, nil })
				return
			}
			Handle(NewRouter(), spec, h)
		}()
	}
}
