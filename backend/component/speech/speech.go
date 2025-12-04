package speech

import (
	"context"

	"github.com/sundbassen/alfred/component/command"
)

type SpeechResult struct {
	Text       command.Command // Transcribed text
	Confidence float32         // Confidence score of the transcription
	Answer     []byte          // Optional answer audio data
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
