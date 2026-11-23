// Package whisperprocessor transcribes command audio locally with
// whisper.cpp.
package whisperprocessor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/sundbassen/alfred/component/speech"
	"github.com/sundbassen/alfred/internal/pcm"
)

type processor struct {
	whisperContext whisper.Context
	sampleRate     int
	logger         *slog.Logger
}

type Config struct {
	Model      whisper.Model // Whisper model instance (e.g. models/ggml-base.en.bin)
	Language   string        // Language code (e.g., "en"); empty or "auto" detects; ignored by .en models
	Threads    uint          // Decoder threads (default 4)
	SampleRate int           // Sample rate of input audio (whisper requires 16000)
	Logger     *slog.Logger  // Logger instance
}

func New(config Config) speech.Processor {
	ctx, err := config.Model.NewContext()
	if err != nil {
		config.Logger.Error("Failed to create whisper context for processor", slog.Any("error", err))
		return nil
	}

	// English-only (.en) models reject SetLanguage; they need none.
	if ctx.IsMultilingual() && config.Language != "" && config.Language != "auto" {
		if err := ctx.SetLanguage(config.Language); err != nil {
			config.Logger.Error("Failed to set whisper language",
				slog.String("language", config.Language), slog.Any("error", err))
			return nil
		}
	}

	threads := config.Threads
	if threads == 0 {
		threads = 4
	}

	// Commands are short: greedy, deterministic decoding with no carried
	// text context is both fastest and avoids one command's transcript
	// biasing the next.
	ctx.SetBeamSize(1)
	ctx.SetTemperature(0)
	ctx.SetThreads(threads)
	ctx.SetMaxContext(0)
	ctx.SetSplitOnWord(false)

	return &processor{
		whisperContext: ctx,
		sampleRate:     config.SampleRate,
		logger:         config.Logger,
	}
}

func (p *processor) Process(ctx context.Context, data []byte) (speech.SpeechResult, error) {
	if len(data) == 0 {
		return speech.SpeechResult{}, nil
	}

	start := time.Now()
	if err := p.whisperContext.Process(pcm.ToFloat32Normalized(data), nil, nil, nil); err != nil {
		return speech.SpeechResult{}, fmt.Errorf("whisper process: %w", err)
	}

	var text strings.Builder
	var probSum float64
	var tokens int
	for {
		segment, err := p.whisperContext.NextSegment()
		if err != nil {
			break
		}
		text.WriteString(segment.Text)
		for _, token := range segment.Tokens {
			if p.whisperContext.IsText(token) {
				probSum += float64(token.P)
				tokens++
			}
		}
	}

	var confidence float32
	if tokens > 0 {
		confidence = float32(probSum / float64(tokens))
	}

	result := speech.SpeechResult{
		Text:       strings.TrimSpace(text.String()),
		Confidence: confidence,
	}

	p.logger.Info("Command transcribed",
		slog.String("text", result.Text),
		slog.Float64("confidence", float64(confidence)),
		slog.Float64("audio_seconds", pcm.BytesToSeconds(len(data), p.sampleRate)),
		slog.Duration("took", time.Since(start)))

	return result, nil
}
