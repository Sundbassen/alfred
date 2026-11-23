package whisperactivator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/sundbassen/alfred/component/speech"
	"github.com/sundbassen/alfred/internal/pcm"
)

type activator struct {
	activateOn     string
	model          whisper.Model
	whisperContext whisper.Context
	language       string
	sampleRate     int
	logger         *slog.Logger
}

type Config struct {
	ActivateOn string        // The keyword/phrase to activate on (e.g., "hey alfred")
	Model      whisper.Model // Whisper model instance (shared or dedicated)
	Language   string        // Language code (e.g., "en")
	SampleRate int           // Sample rate of input audio (must match microphone)
	Logger     *slog.Logger  // Logger instance
}

func New(config Config) speech.Activator {
	// Create a persistent Whisper context optimized for activation detection
	ctx, err := config.Model.NewContext()
	if err != nil {
		config.Logger.Error("Failed to create whisper context for activator", slog.Any("error", err))
		return nil
	}

	// Set language if specified
	if config.Language != "auto" && config.Language != "" {
		ctx.SetLanguage(config.Language)
	}

	// Optimize for speed and bias toward the activation phrase
	ctx.SetBeamSize(1)            // Greedy search - fastest decoding
	ctx.SetTemperature(0)         // Deterministic output
	ctx.SetThreads(2)             // Fewer threads for wake word detection
	ctx.SetSplitOnWord(false)     // Faster processing
	ctx.SetTokenTimestamps(false) // Disable for speed
	ctx.SetMaxContext(0)          // Reduce context window for speed
	ctx.SetAudioCtx(0)            // Use default audio context

	// Set initial prompt to bias the model toward the activation phrase
	// This significantly improves recognition of the specific phrase
	// Repeat the wake word multiple times to strongly bias the model
	promptText := fmt.Sprintf(`Listen "%s" You have one purpose, which is to listen for the activation word. %s
	rather false positives than false negatives.`, config.ActivateOn, config.ActivateOn)
	ctx.SetInitialPrompt(promptText)

	return &activator{
		activateOn:     strings.ToLower(config.ActivateOn),
		model:          config.Model,
		whisperContext: ctx,
		language:       config.Language,
		sampleRate:     config.SampleRate,
		logger:         config.Logger,
	}
}

func (a *activator) Activate(ctx context.Context, data []byte) (bool, error) {
	// Convert audio bytes to text
	text := a.transcribeAudio(data)

	// Normalize and check if the transcribed text contains the activation phrase
	normalizedText := strings.ToLower(strings.TrimSpace(text))

	// Remove common punctuation that might interfere
	normalizedText = strings.ReplaceAll(normalizedText, ".", "")
	normalizedText = strings.ReplaceAll(normalizedText, ",", "")
	normalizedText = strings.ReplaceAll(normalizedText, "!", "")
	normalizedText = strings.ReplaceAll(normalizedText, "?", "")

	activated := strings.Contains(normalizedText, a.activateOn)

	if activated {
		a.logger.Info("Activation phrase detected",
			slog.String("text", text),
			slog.String("normalized", normalizedText),
			slog.String("activation_phrase", a.activateOn))
	}
	a.logger.Info("Audio activation transcription",
		slog.String("text", text),
		slog.String("normalized", normalizedText),
		slog.String("activation_phrase", a.activateOn))
	return activated, nil
}

// transcribeAudio performs the actual transcription using Whisper
func (a *activator) transcribeAudio(audioData []byte) string {
	if len(audioData) == 0 {
		return ""
	}

	// Convert byte data (int16) to float32 samples as required by Whisper
	samples := pcm.ToFloat32Normalized(audioData)

	a.logger.Debug("Activator transcribing audio", slog.Int("samples", len(samples)))

	// Reuse the persistent context - it's already configured with optimal settings
	// and the initial prompt biasing toward the activation phrase
	if err := a.whisperContext.Process(samples, nil, nil, nil); err != nil {
		a.logger.Error("Failed to process audio samples for activation", slog.Any("error", err))
		return ""
	}

	// Collect all segments
	var fullText string

	for {
		segment, err := a.whisperContext.NextSegment()
		if err != nil {
			break
		}

		if segment.Text != "" {
			fullText += segment.Text
		}
	}

	return strings.TrimSpace(fullText)
}
