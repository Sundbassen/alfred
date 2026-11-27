// Package tts speaks Alfred's responses.
package tts

import (
	"context"
	"log/slog"
)

// Speaker says text out loud. Speak blocks until playback finishes.
type Speaker interface {
	Speak(ctx context.Context, text string) error
}

// Synthesizer turns text into speech audio, as WAV file bytes, without
// playing it: for a hub that sends the audio to a satellite.
type Synthesizer interface {
	Synthesize(ctx context.Context, text string) ([]byte, error)
}

type logSpeaker struct {
	logger *slog.Logger
}

// NewLogSpeaker returns a Speaker that only logs the text, for headless use
// or when no TTS engine is installed.
func NewLogSpeaker(logger *slog.Logger) Speaker {
	return &logSpeaker{logger: logger}
}

func (s *logSpeaker) Speak(ctx context.Context, text string) error {
	s.logger.Info("🔇 Would speak", slog.String("text", text))
	return nil
}
