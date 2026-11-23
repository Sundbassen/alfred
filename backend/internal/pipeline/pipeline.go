// Package pipeline builds the speech-to-text service from its components,
// shared by the live app (cmd/alfred) and the offline replay harness
// (cmd/replaywav).
package pipeline

import (
	"fmt"
	"log/slog"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/sundbassen/alfred/component/command"
	"github.com/sundbassen/alfred/component/command/clock"
	"github.com/sundbassen/alfred/component/debounce"
	"github.com/sundbassen/alfred/component/debounce/silerovad"
	"github.com/sundbassen/alfred/component/speech"
	"github.com/sundbassen/alfred/component/speech/activate/onnxactivator"
	"github.com/sundbassen/alfred/component/speech/activate/recordingactivator"
	"github.com/sundbassen/alfred/component/speech/process/whisperprocessor"
	"github.com/sundbassen/alfred/component/speechtotext"
)

const (
	SampleRate = 16000

	onnxRuntimeLib = "/tmp/onnxruntime/lib/libonnxruntime.so"
	whisperModel   = "models/ggml-base.en.bin"
	sileroModel    = "models/silero/silero_vad.onnx"
	recordingsDir  = ".recordings"

	// Silence that ends a command (or, in record mode, an utterance).
	silenceMs = 600
)

type Options struct {
	// Record selects the legacy utterance mode: every silence-delimited
	// utterance is scored by the batch wake word activator and saved as a
	// wav file for training. No commands are transcribed.
	Record bool
	// VAD selects the silence detector: "rms" (adaptive RMS) or "silero".
	VAD string
	// Commands, if set, biases transcription toward their phrases via
	// whisper's initial prompt.
	Commands *command.Registry
}

// Commands returns the registry of everything Alfred can do. Register new
// commands here.
func Commands() *command.Registry {
	return command.NewRegistry(
		clock.New(),
	)
}

// New builds the speech-to-text service. The returned cleanup releases the
// whisper model and must be called after the service has stopped.
func New(opts Options, logger *slog.Logger) (speechtotext.SpeechToTextService, func(), error) {
	detector, err := newSilenceDetector(opts.VAD, logger)
	if err != nil {
		return nil, nil, err
	}

	activatorConfig := onnxactivator.Config{
		ModelPath:          "models/alfred/hey_alfred.onnx",
		MelspecModelPath:   "models/openwakeword/melspectrogram.onnx",
		EmbeddingModelPath: "models/openwakeword/embedding_model.onnx",
		OnnxRuntimeLibPath: onnxRuntimeLib,
		Threshold:          0.03,
		SampleRate:         SampleRate,
		Logger:             logger,
	}

	if opts.Record {
		return newRecordMode(activatorConfig, detector, logger)
	}

	streaming := onnxactivator.NewStreaming(activatorConfig)
	if streaming == nil {
		return nil, nil, fmt.Errorf("failed to create streaming wake word activator")
	}

	model, err := whisper.New(whisperModel)
	if err != nil {
		return nil, nil, fmt.Errorf("load whisper model %s: %w", whisperModel, err)
	}
	cleanup := func() { model.Close() }

	var prompt string
	if opts.Commands != nil {
		prompt = opts.Commands.Prompt()
	}
	processor := whisperprocessor.New(whisperprocessor.Config{
		Model:         model,
		Language:      "en",
		SampleRate:    SampleRate,
		Logger:        logger,
		InitialPrompt: prompt,
	})
	if processor == nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to create whisper processor")
	}

	config := speechtotext.DefaultConfig(detector, speech.NewSpeechProcessor(processor, nil))
	config.StreamingActivator = streaming
	config.CommandSilenceMs = silenceMs

	service, err := speechtotext.NewSTTService(config, logger)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return service, cleanup, nil
}

func newRecordMode(activatorConfig onnxactivator.Config, detector debounce.SilenceDetector,
	logger *slog.Logger,
) (speechtotext.SpeechToTextService, func(), error) {
	activator := onnxactivator.New(activatorConfig)
	if activator == nil {
		return nil, nil, fmt.Errorf("failed to create wake word activator")
	}

	// Save every scored utterance as a wav file for wake word training
	// (--custom-samples)
	activator = recordingactivator.New(recordingactivator.Config{
		Inner:      activator,
		Dir:        recordingsDir,
		SampleRate: SampleRate,
		Logger:     logger,
	})
	if activator == nil {
		return nil, nil, fmt.Errorf("failed to create recording activator")
	}

	config := speechtotext.DefaultConfig(detector,
		speech.NewSpeechProcessor(speech.NewPlaceholderProcessor(), activator))
	config.SilenceDuration = silenceMs

	service, err := speechtotext.NewSTTService(config, logger)
	if err != nil {
		return nil, nil, err
	}
	return service, func() {}, nil
}

func newSilenceDetector(vad string, logger *slog.Logger) (debounce.SilenceDetector, error) {
	switch vad {
	case "", "rms":
		return debounce.NewRMSSilenceDetector(debounce.RMSConfig{
			// RMS of normalized samples; the minimum threshold, raised
			// by the adaptive noise floor in louder rooms. Tune with the
			// "Audio level stats" log: must sit above ambient rms_avg and
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
		return silerovad.New(silerovad.Config{
			ModelPath:          sileroModel,
			OnnxRuntimeLibPath: onnxRuntimeLib,
			SampleRate:         SampleRate,
			Logger:             logger,
		})
	default:
		return nil, fmt.Errorf("unknown -vad %q (want rms or silero)", vad)
	}
}
