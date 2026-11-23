// Package stt transcribes command audio locally with
// whisper.cpp.
package stt

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/sundbassen/alfred/internal/platform/pcm"
	"github.com/sundbassen/alfred/internal/voice"
	"gitlab.com/tozd/go/errors"
)

type processor struct {
	config Config
}

type Config struct {
	Model      whisper.Model // Whisper model instance (e.g. models/ggml-base.en.bin)
	Language   string        // Language code (e.g., "en"); empty or "auto" detects; ignored by .en models
	Threads    uint          // Decoder threads (default 4)
	SampleRate int           // Sample rate of input audio (whisper requires 16000)
	Logger     *slog.Logger  // Logger instance

	// InitialPrompt biases decoding toward the expected vocabulary, e.g.
	// the known command phrases. Empty means no prompt.
	InitialPrompt string
}

// maxTokens caps a transcript's length. Commands are a handful of tokens;
// the cap bounds the latency of a decoding loop.
const maxTokens = 48

func New(config Config) voice.Transcriber {
	if config.Threads == 0 {
		config.Threads = 4
	}
	p := &processor{config: config}
	// Fail at startup, not on the first command, if the context cannot be
	// configured (e.g. an unsupported language).
	if _, err := p.newContext(); err != nil {
		config.Logger.Error("Failed to create whisper context for processor", slog.Any("error", err))
		return nil
	}
	return p
}

// newContext returns a freshly configured whisper context. Each command
// gets its own: the binding's context never resets its NextSegment cursor
// between Process calls, so a reused context returns an empty transcript
// for every command after the first. Contexts only hold parameters (the
// decoder state lives in the model), so creating one is cheap.
func (p *processor) newContext() (whisper.Context, error) {
	ctx, err := p.config.Model.NewContext()
	if err != nil {
		return nil, err
	}

	// English-only (.en) models reject SetLanguage; they need none.
	if lang := p.config.Language; ctx.IsMultilingual() && lang != "" && lang != "auto" {
		if err := ctx.SetLanguage(lang); err != nil {
			return nil, errors.WithMessagef(err, "set whisper language %q", lang)
		}
	}

	// Commands are short: deterministic decoding. model.NewContext sets
	// no_context, which clears the text history on every Process call, so
	// one command's transcript cannot bias the next. Do not SetMaxContext(0)
	// for that: whisper.cpp then drops the initial prompt as well.
	//
	// The Go binding always decodes greedily (model.NewContext hardcodes
	// SAMPLING_GREEDY, so SetBeamSize is a no-op); the initial prompt
	// compensates: greedy heard "what time is it" as "what I visit"
	// without it and correctly with it.
	ctx.SetTemperature(0)
	ctx.SetThreads(p.config.Threads)
	ctx.SetSplitOnWord(false)
	ctx.SetMaxTokensPerSegment(maxTokens)
	if p.config.InitialPrompt != "" {
		ctx.SetInitialPrompt(p.config.InitialPrompt)
	}
	return ctx, nil
}

func (p *processor) Transcribe(ctx context.Context, data []byte) (voice.Transcript, error) {
	if len(data) == 0 {
		return voice.Transcript{}, nil
	}

	whisperContext, err := p.newContext()
	if err != nil {
		return voice.Transcript{}, errors.WithMessage(err, "whisper context")
	}

	start := time.Now()
	if err := whisperContext.Process(pcm.ToFloat32Normalized(data), nil, nil, nil); err != nil {
		return voice.Transcript{}, errors.WithMessage(err, "whisper process")
	}

	var text strings.Builder
	var probSum float64
	var tokens int
	for {
		segment, err := whisperContext.NextSegment()
		if err != nil {
			break
		}
		text.WriteString(segment.Text)
		for _, token := range segment.Tokens {
			if whisperContext.IsText(token) {
				probSum += float64(token.P)
				tokens++
			}
		}
	}

	var confidence float64
	if tokens > 0 {
		confidence = probSum / float64(tokens)
	}

	result := voice.Transcript{
		Text:       strings.TrimSpace(text.String()),
		Confidence: confidence,
	}

	p.config.Logger.Info("Command transcribed",
		slog.String("text", result.Text),
		slog.Float64("confidence", confidence),
		slog.Float64("audio_seconds", pcm.BytesToSeconds(len(data), p.config.SampleRate)),
		slog.Duration("took", time.Since(start)))

	return result, nil
}
