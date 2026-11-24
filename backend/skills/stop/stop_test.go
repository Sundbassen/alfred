package stop

import (
	"context"
	"testing"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

// fakeStopper reports a scripted result and counts calls.
type fakeStopper struct {
	stopped bool
	err     error
	calls   int
}

func (f *fakeStopper) Stop(context.Context) (bool, error) {
	f.calls++
	return f.stopped, f.err
}

func TestStopsTheFirstThatIsGoingOn(t *testing.T) {
	idle, ringing, music := &fakeStopper{}, &fakeStopper{stopped: true}, &fakeStopper{stopped: true}
	r := assistant.NewRouter()
	Register(r, idle, ringing, music)
	resp, err := r.Dispatch(context.Background(), Stop{})
	if err != nil || resp.Text != "" {
		t.Errorf("resp %+v, err %v; want silence", resp, err)
	}
	if idle.calls != 1 || ringing.calls != 1 || music.calls != 0 {
		t.Errorf("calls: idle %d, ringing %d, music %d; want 1, 1, 0", idle.calls, ringing.calls, music.calls)
	}
}

func TestNothingToStop(t *testing.T) {
	r := assistant.NewRouter()
	Register(r, &fakeStopper{})
	if resp, err := r.Dispatch(context.Background(), Stop{}); err != nil || resp.Text != NothingText {
		t.Errorf("resp %+v, err %v", resp, err)
	}
}

func TestStopperError(t *testing.T) {
	r := assistant.NewRouter()
	Register(r, StopperFunc(func(context.Context) (bool, error) {
		return true, errors.WithMessage(fault.ErrServiceUnavailable, "spotify")
	}))
	if _, err := r.Dispatch(context.Background(), Stop{}); !errors.Is(err, fault.ErrServiceUnavailable) {
		t.Errorf("err = %v", err)
	}
}

func TestPhrases(t *testing.T) {
	r := assistant.NewRouter()
	Register(r)
	m := match.New(r.Intents())
	for _, transcript := range []string{"Stop.", "Hey Alfred, stop!", "Stop the alarm.", "Turn it off."} {
		if rec, err := m.Recognize(context.Background(), transcript); err != nil || rec.Intent != "stop" {
			t.Errorf("%q: %+v, %v", transcript, rec, err)
		}
	}
}
