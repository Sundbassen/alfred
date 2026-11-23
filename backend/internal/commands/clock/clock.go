// Package clock tells the time: the "time" intent.
package clock

import (
	"context"
	"fmt"
	"time"

	"github.com/sundbassen/alfred/internal/assistant"
)

// AskTime is the intent "what time is it".
type AskTime struct{}

func (AskTime) IntentName() string { return "time" }

// Register adds the clock's intents to r.
func Register(r *assistant.Router) {
	register(r, time.Now)
}

func register(r *assistant.Router, now func() time.Time) {
	h := &handler{now: now}
	assistant.Handle(r, assistant.Spec{
		Description: "Tell the current local time",
		Phrases: []string{
			"what time is it",
			"what is the time",
			"what is the time now",
			"tell me the time",
			"current time",
		},
	}, h.askTime)
}

type handler struct {
	now func() time.Time
}

func (h *handler) askTime(ctx context.Context, _ AskTime) (assistant.Response, error) {
	return assistant.Response{Text: fmt.Sprintf("It's %s.", h.now().Format("15:04"))}, nil
}
