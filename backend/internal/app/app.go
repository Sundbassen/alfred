// Package app wires the voice framework (internal/voice) into a listener
// and answers transcripts with the registered skills (Assistant). It never
// imports skills: the binaries register them through cmd/internal/wiring.
// Shared by the live app (cmd/alfred) and the offline replay harness
// (cmd/replaywav).
package app

import (
	"log/slog"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/platform/onnxrt"
	"github.com/sundbassen/alfred/internal/voice"
	"github.com/sundbassen/alfred/internal/voice/listener"
	"github.com/sundbassen/alfred/internal/voice/recorder"
	"github.com/sundbassen/alfred/internal/voice/stt"
	"github.com/sundbassen/alfred/internal/voice/vad"
	"github.com/sundbassen/alfred/internal/voice/vad/silero"
	"github.com/sundbassen/alfred/internal/voice/wakeword"
	"gitlab.com/tozd/go/errors"
)

const (
	SampleRate = 16000

	whisperModel = "models/ggml-base.en.bin"
	sileroModel  = "models/silero/silero_vad.onnx"

	// Silence that ends a assistant.
	silenceMs = 600
)

type Options struct {
	// VAD selects the silence detector: "rms" (adaptive RMS) or "silero".
	VAD string
	// Prompt, if set, biases transcription toward the command phrases via
	// whisper's initial prompt (Assistant.Prompt).
	Prompt string
	// SaveDir, if set, saves wake word clips, near misses and commands
	// under this directory (see internal/voice/recorder).
	SaveDir string
	// NearMissThreshold: wake word score from which an undetected
	// utterance is saved as a near miss (0 = default 0.005).
	NearMissThreshold float32
	// Verbose keeps whisper.cpp's own log output (model loading details),
	// which is discarded otherwise.
	Verbose bool
}

// New builds the speech-to-text service. The returned cleanup releases the
// whisper model and must be called after the service has stopped.
func New(opts Options, logger *slog.Logger) (listener.Service, func(), error) {
	detector, err := newSilenceDetector(opts.VAD, logger)
	if err != nil {
		return nil, nil, err
	}

	activatorConfig := wakeword.Config{
		ModelPath:          "models/alfred/hey_alfred.onnx",
		MelspecModelPath:   "models/openwakeword/melspectrogram.onnx",
		EmbeddingModelPath: "models/openwakeword/embedding_model.onnx",
		OnnxRuntimeLibPath: onnxrt.LibPath(),
		Threshold:          0.03,
		SampleRate:         SampleRate,
		Logger:             logger,
	}

	streaming := wakeword.NewStreaming(activatorConfig)
	if streaming == nil {
		return nil, nil, errors.WithMessage(fault.ErrInternal, "create streaming wake word activator")
	}

	processor, cleanup, err := NewTranscriber(opts, logger)
	if err != nil {
		return nil, nil, err
	}

	config := listener.DefaultConfig(detector, processor, streaming)
	config.CommandSilenceMs = silenceMs
	config.NearMissThreshold = opts.NearMissThreshold
	if opts.SaveDir != "" {
		rec, err := recorder.New(opts.SaveDir, SampleRate, logger)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		config.Sink = rec
		modelCleanup := cleanup
		cleanup = func() {
			rec.Close()
			modelCleanup()
		}
		logger.Warn("Saving wake word clips, near misses and commands to disk",
			slog.String("dir", opts.SaveDir))
	}

	service, err := listener.New(config, logger)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return service, cleanup, nil
}

// NewTranscriber loads the whisper model and returns its transcriber,
// biased toward opts.Prompt. The cleanup releases the model. It is shared
// by the live app (New) and the hub, which transcribes satellites' audio.
func NewTranscriber(opts Options, logger *slog.Logger) (voice.Transcriber, func(), error) {
	if !opts.Verbose {
		stt.QuietLogs()
	}
	model, err := whisper.New(whisperModel)
	if err != nil {
		return nil, nil, errors.WithMessagef(err, "load whisper model %s", whisperModel)
	}
	processor := stt.New(stt.Config{
		Model:         model,
		Language:      "en",
		SampleRate:    SampleRate,
		Logger:        logger,
		InitialPrompt: opts.Prompt,
	})
	if processor == nil {
		model.Close()
		return nil, nil, errors.WithMessage(fault.ErrInternal, "create whisper processor")
	}
	return processor, func() { model.Close() }, nil
}

// CommandSilenceMs is the silence that ends a command, for listeners built
// outside New (the hub's).
const CommandSilenceMs = silenceMs

// NewSilenceDetector returns the silence detector by kind: "rms" (default)
// or "silero". Each listener needs its own (they keep state).
func NewSilenceDetector(kind string, logger *slog.Logger) (voice.SilenceDetector, error) {
	return newSilenceDetector(kind, logger)
}

func newSilenceDetector(kind string, logger *slog.Logger) (voice.SilenceDetector, error) {
	switch kind {
	case "", "rms":
		return vad.NewRMSSilenceDetector(vad.RMSConfig{
			// RMS of normalized samples; the minimum threshold, raised
			// by the adaptive noise floor in louder rooms. Tune with the
			// "Audio level stats" log (alfred -v): must sit above ambient rms_avg and
			// below speech rms_max. Measured 2026-07-19 with mic source
			// at 25% volume: ambient ~0.004.
			SilenceThreshold: 0.015,
			// Footsteps and rumble are low-frequency; speech is not.
			// The high-pass keeps walking noise from counting as speech.
			HighPassHz: 200,
			SampleRate: SampleRate,
			Adaptive:   true,
			Logger:     logger,
		}), nil
	case "silero":
		return silero.New(silero.Config{
			ModelPath:          sileroModel,
			OnnxRuntimeLibPath: onnxrt.LibPath(),
			SampleRate:         SampleRate,
			Logger:             logger,
		})
	default:
		return nil, errors.WithMessagef(fault.ErrInvalidValue, "unknown -vad %q (want rms or silero)", kind)
	}
}
