// Package stop is the stop skill: "stop" stops whatever needs stopping
// most, e.g. a ringing timer before the music. It owns the bare "stop"
// intent and coordinates the other skills through the Stopper interface;
// cmd/internal/wiring passes them in order.
package stop

import (
	"context"

	"github.com/sundbassen/alfred/internal/assistant"
)

// Stop is "stop": stop the most urgent thing going on.
type Stop struct{}

func (Stop) IntentName() string { return "stop" }

// Stopper stops something if it is going on, reporting whether it was.
type Stopper interface {
	Stop(ctx context.Context) (stopped bool, err error)
}

// StopperFunc adapts a function to a Stopper.
type StopperFunc func(ctx context.Context) (bool, error)

func (f StopperFunc) Stop(ctx context.Context) (bool, error) { return f(ctx) }

// NothingText is the reply when nothing was going on.
const NothingText = "There's nothing to stop."

// Register adds the stop intent to r. stoppers are tried in order until
// one stopped something: put the most urgent first (ringing timers before
// music).
func Register(r *assistant.Router, stoppers ...Stopper) {
	assistant.Handle(r, assistant.Spec{
		Description: "Stop whatever is going on: a ringing timer, alarm or reminder, or the music",
		Phrases: []string{
			"stop",
			"stop it",
			"stop the alarm",
			"stop the timer",
			"stop the reminder",
			"dismiss",
			"turn it off",
		},
	}, func(ctx context.Context, _ Stop) (assistant.Response, error) {
		for _, s := range stoppers {
			stopped, err := s.Stop(ctx)
			if err != nil {
				return assistant.Response{}, err
			}
			if stopped {
				// Going quiet is the answer.
				return assistant.Response{}, nil
			}
		}
		return assistant.Response{Text: NothingText}, nil
	})
}
