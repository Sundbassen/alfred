package assistant

import (
	"context"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

// FailedText is what to say about a failure without a more specific phrase.
const FailedText = "Sorry, something went wrong."

// SpokenText returns what to say about a failed intent: a SpokenError's
// Text, otherwise a phrase for its fault category, otherwise FailedText.
// This is the voice front end's single mapping from errors to speech (like
// an HTTP front end's mapping from errors to status codes).
func SpokenText(err error) string {
	var spoken *SpokenError
	if errors.As(err, &spoken) {
		return spoken.Text
	}
	switch {
	case errors.Is(err, fault.ErrServiceUnavailable):
		return "Sorry, I can't reach that right now."
	case errors.Is(err, fault.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return "Sorry, that took too long."
	case errors.Is(err, fault.ErrTemporary):
		return "Sorry, that didn't work. Try again in a moment."
	case errors.Is(err, fault.ErrNotFound):
		return "Sorry, I couldn't find that."
	}
	return FailedText
}
