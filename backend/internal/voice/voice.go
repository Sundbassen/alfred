// Package voice holds the contracts between the voice framework's parts:
// the listener (internal/voice/listener) drives a wake word detector, a
// silence detector and a transcriber, implemented by internal/voice/
// wakeword, internal/voice/vad and internal/voice/stt.
package voice

import "context"

// Transcript is the text of one command utterance.
type Transcript struct {
	Text       string
	Confidence float64 // 0..1, mean probability of the text tokens
}

// Transcriber turns a complete command utterance (16 kHz mono PCM16) into
// text.
type Transcriber interface {
	Transcribe(ctx context.Context, pcm []byte) (Transcript, error)
}

// WakeWordDetector detects the wake word in continuous audio. It is fed
// every microphone chunk in order and can fire mid-utterance.
type WakeWordDetector interface {
	// Feed consumes the next chunk of continuous PCM16 audio (any size) and
	// reports whether the wake word was detected. score is the highest
	// wake word score of the chunk (0 if it completed no scoring step), for
	// near-miss tracking.
	Feed(ctx context.Context, chunk []byte) (score float32, detected bool, err error)

	// Reset clears buffered audio and features, e.g. after a detection, so
	// earlier audio cannot trigger again.
	Reset()
}

// SilenceDetector classifies audio chunks as silence or speech.
type SilenceDetector interface {
	IsSilent(ctx context.Context, chunk []byte) bool
}
