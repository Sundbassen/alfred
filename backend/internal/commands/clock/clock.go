// Package clock implements the "what time is it" assistant.
package clock

import (
	"context"
	"fmt"
	"time"

	"github.com/sundbassen/alfred/internal/assistant"
)

type clock struct {
	now func() time.Time
}

// New returns the command that tells the current local time.
func New() assistant.Command {
	return newWithNow(time.Now)
}

func newWithNow(now func() time.Time) assistant.Command {
	return &clock{now: now}
}

func (c *clock) ID() string { return "time" }

func (c *clock) Description() string { return "Tell the current local time" }

func (c *clock) Phrases() []string {
	return []string{
		"what time is it",
		"what is the time",
		"what is the time now",
		"tell me the time",
		"current time",
	}
}

func (c *clock) Execute(ctx context.Context, req assistant.Request) (assistant.Response, error) {
	return assistant.Response{Text: fmt.Sprintf("It's %s.", c.now().Format("15:04"))}, nil
}
