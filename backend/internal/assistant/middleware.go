package assistant

import (
	"context"
	"errors"
)

// FailedText is said when a handler fails without a SpokenError.
const FailedText = "Sorry, something went wrong."

// SpeakErrors gives every failure something to say: a SpokenError's Text,
// otherwise FailedText. The error is still returned, for logging; callers
// speak Response.Text whenever it is set, error or not.
func SpeakErrors(next Handler) Handler {
	return HandlerFunc(func(ctx context.Context, intent Intent) (Response, error) {
		resp, err := next.Handle(ctx, intent)
		if err == nil {
			return resp, nil
		}
		var spoken *SpokenError
		if errors.As(err, &spoken) {
			return Response{Text: spoken.Text}, err
		}
		return Response{Text: FailedText}, err
	})
}
