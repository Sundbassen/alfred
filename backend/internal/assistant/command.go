// Package assistant defines the things Alfred can do (Command) and maps a
// transcribed utterance to one of them (Registry). The commands themselves
// live in internal/commands.
package assistant

import "context"

// Command is one thing Alfred can do.
type Command interface {
	// ID is a stable identifier, e.g. "time". It will also serve as the
	// tool enum value for the cloud LLM fallback.
	ID() string

	// Description is a one-line summary of what the command does.
	Description() string

	// Phrases are example phrasings used by the local matcher.
	Phrases() []string

	// Execute performs the command.
	Execute(ctx context.Context, req Request) (Response, error)
}

// Request is the input to a command.
type Request struct {
	Transcript string // raw transcript as heard
}

// Response is what a command produces.
type Response struct {
	Text string // what Alfred says back (logged for now, spoken once TTS exists)
}
