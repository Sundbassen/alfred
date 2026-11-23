package speech

import (
	"context"
)

type SpeechResult struct {
	Text       string  // Transcribed text
	Confidence float32 // Confidence score of the transcription
	Answer     []byte  // Optional answer audio data
}

type Processor interface {
	Process(ctx context.Context, data []byte) (SpeechResult, error)
}

type Activator interface {
	Activate(ctx context.Context, data []byte) (bool, error)
}

type SpeechProcessor interface {
	Processor
	Activator
}

type speechProcessor struct {
	Processor
	Activator
}

func NewSpeechProcessor(processor Processor, activator Activator) SpeechProcessor {
	return &speechProcessor{
		Processor: processor,
		Activator: activator,
	}
}

// placeholderProcessor is a simple placeholder implementation of Processor
type placeholderProcessor struct{}

func NewPlaceholderProcessor() Processor {
	return &placeholderProcessor{}
}

func (p *placeholderProcessor) Process(ctx context.Context, data []byte) (SpeechResult, error) {
	// Placeholder implementation - returns empty result
	return SpeechResult{
		Text:       "",
		Confidence: 0.0,
		Answer:     nil,
	}, nil
}

// StreamingActivator detects the wake word in continuous audio. Unlike
// Activator, which scores a complete utterance, it is fed every microphone
// chunk in order and can fire mid-utterance.
type StreamingActivator interface {
	// Feed consumes the next chunk of continuous PCM16 audio (any size) and
	// reports whether the wake word was detected.
	Feed(ctx context.Context, chunk []byte) (bool, error)

	// Reset clears buffered audio and features, e.g. after a detection, so
	// earlier audio cannot trigger again.
	Reset()
}
