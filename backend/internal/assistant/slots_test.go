package assistant

import (
	"context"
	"testing"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

type play struct {
	Query string `slot:"query"`
	Other int    // untagged fields are left alone
}

func (play) IntentName() string { return "play" }

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
