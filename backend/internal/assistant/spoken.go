package assistant

import "errors"

// FailedText is what to say about a handler failure without a SpokenError.
const FailedText = "Sorry, something went wrong."

// SpokenText returns what to say about a failed intent: a SpokenError's
// Text, otherwise FailedText. Turning errors into speech is the voice
// front end's job; another front end (e.g. HTTP) maps the same errors its
// own way.
func SpokenText(err error) string {
	var spoken *SpokenError
	if errors.As(err, &spoken) {
		return spoken.Text
	}
	return FailedText
}
