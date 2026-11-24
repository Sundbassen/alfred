// Package conversation is the conversation skill: the small talk around
// commands. "Never mind" ends things quietly, "repeat that" says the last
// reply again, and "what can you do" lists the skills' help phrases
// (assistant.Spec.Help).
package conversation

import (
	"context"
	"strings"
	"sync"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/spoken"
)

// NeverMind is "never mind": say nothing.
type NeverMind struct{}

func (NeverMind) IntentName() string { return "never_mind" }

// Repeat is "repeat that": say the last reply again.
type Repeat struct{}

func (Repeat) IntentName() string { return "repeat" }

// Help is "what can you do".
type Help struct{}

func (Help) IntentName() string { return "help" }

// History remembers the last thing Alfred said, for Repeat. Install its
// Middleware on the router so it sees every reply.
type History struct {
	mu   sync.Mutex
	last string
}

// NewHistory returns an empty History.
func NewHistory() *History { return &History{} }

// Middleware records each non-empty reply.
func (h *History) Middleware(next assistant.Handler) assistant.Handler {
	return assistant.HandlerFunc(func(ctx context.Context, intent assistant.Intent) (assistant.Response, error) {
		resp, err := next.Handle(ctx, intent)
		if err == nil && resp.Text != "" {
			h.mu.Lock()
			h.last = resp.Text
			h.mu.Unlock()
		}
		return resp, err
	})
}

// Last returns the last reply, or "" if there was none.
func (h *History) Last() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

// Register adds the conversation intents to r. "What can you do" reads the
// registered intents when asked, so it covers skills registered later.
func Register(r *assistant.Router, history *History) {
	assistant.Handle(r, assistant.Spec{
		Description: "Dismiss: the user changed their mind or wants nothing",
		Phrases:     []string{"never mind", "forget it", "cancel", "nothing", "no thanks"},
	}, func(context.Context, NeverMind) (assistant.Response, error) {
		return assistant.Response{}, nil
	})
	assistant.Handle(r, assistant.Spec{
		Description: "Repeat what Alfred said last",
		Phrases:     []string{"repeat that", "say that again", "what did you say", "repeat"},
	}, func(context.Context, Repeat) (assistant.Response, error) {
		if last := history.Last(); last != "" {
			return assistant.Response{Text: last}, nil
		}
		return assistant.Response{Text: "I haven't said anything yet."}, nil
	})
	assistant.Handle(r, assistant.Spec{
		Description: "Say what Alfred can do",
		Phrases:     []string{"what can you do", "help", "what can i ask you", "what can i say"},
	}, func(context.Context, Help) (assistant.Response, error) {
		var helps []string
		for _, rt := range r.Intents() {
			if rt.Spec.Help != "" {
				helps = append(helps, strings.TrimSuffix(rt.Spec.Help, "."))
			}
		}
		if len(helps) == 0 {
			return assistant.Response{Text: "Not much yet."}, nil
		}
		return assistant.Response{Text: "I can " + spoken.JoinAnd(helps) + "."}, nil
	})
}
