package speech

import (
	"context"
)

type SpeechResult struct {
	Text       string  // Transcribed text
	Confidence float32 // Confidence score of the transcription
}

// Processor transcribes a complete command utterance.
type Processor interface {
	Process(ctx context.Context, data []byte) (SpeechResult, error)
}

// StreamingActivator detects the wake word in continuous audio. It is fed
// every microphone chunk in order and can fire mid-utterance.
type StreamingActivator interface {
	// Feed consumes the next chunk of continuous PCM16 audio (any size) and
	// reports whether the wake word was detected. score is the highest
	// wake word score of the chunk (0 if it completed no scoring step), for
	// near-miss tracking.
	Feed(ctx context.Context, chunk []byte) (score float32, detected bool, err error)

	// Reset clears buffered audio and features, e.g. after a detection, so
	// earlier audio cannot trigger again.
	Reset()
}
