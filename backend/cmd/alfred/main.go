package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sundbassen/alfred/cmd/internal/wiring"
	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/platform/logging"
	"github.com/sundbassen/alfred/internal/voice/mic"
	"github.com/sundbassen/alfred/internal/voice/tts"
	"github.com/sundbassen/alfred/internal/voice/tts/espeak"
	"gitlab.com/tozd/go/errors"
)

func main() {
	vad := flag.String("vad", "rms", "silence detector: rms (adaptive RMS) or silero (Silero VAD)")
	ttsEngine := flag.String("tts", "espeak", "response voice: espeak (espeak-ng) or none (log only)")
	save := flag.Bool("save", false,
		"save wake word clips, near misses and commands as wav files in .recordings (for training and debugging)")
	nearMiss := flag.Float64("near-miss", 0.005,
		"with -save, wake word score from which an undetected utterance is saved as a near miss")
	verbose := flag.Bool("v", false,
		"verbose: debug logs (audio level and wake word score stats every ~5 s) and whisper.cpp output")
	flag.Parse()

	// Create logger
	logger := logging.New(*verbose)

	speaker, err := newSpeaker(*ttsEngine, logger)
	if err != nil {
		logger.Error("Failed to create speaker", slog.Any("error", err))
		os.Exit(1)
	}

	// Create microphone service with default config
	config := mic.DefaultConfig()
	micService, err := mic.NewMicService(config, logger)
	if err != nil {
		logger.Error("Failed to create microphone service", slog.Any("error", err))
		os.Exit(1)
	}

	// Create the speech pipeline: streaming wake word detection followed by
	// command endpointing and transcription
	router := assistant.NewRouter()
	if err := wiring.RegisterSkills(router, wiring.ConfigFromEnv(), logger); err != nil {
		logger.Error("Failed to set up skills", slog.Any("error", err))
		os.Exit(1)
	}
	asst := app.NewAssistant(router)
	sttService, cleanup, err := app.New(
		app.Options{
			VAD:               *vad,
			Prompt:            asst.Prompt(),
			Verbose:           *verbose,
			SaveDir:           saveDir(*save),
			NearMissThreshold: float32(*nearMiss),
		}, logger)
	if err != nil {
		logger.Error("Failed to create speech pipeline", slog.Any("error", err))
		os.Exit(1)
	}
	defer cleanup()
	defer sttService.Stop()

	// Create context that will be cancelled on interrupt
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start the microphone service
	if err := micService.Start(ctx); err != nil {
		logger.Error("Failed to start microphone service", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("Microphone service started successfully")

	// Start the speech-to-text service
	if err := sttService.Start(ctx); err != nil {
		logger.Error("Failed to start speech-to-text service", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("Speech-to-text service started successfully")

	// Connect mic output to STT input
	go func() {
		for audioData := range micService.AudioChannel() {
			if err := sttService.ProcessAudio(audioData); err != nil {
				logger.Debug("Failed to process audio", slog.Any("error", err))
			}
		}
		logger.Info("Audio channel closed")
	}()

	// Interpret transcriptions as commands and speak the response
	go func() {
		for transcript := range sttService.TranscriptChannel() {
			if transcript.Text == "" {
				continue
			}
			logger.Info("📝 TRANSCRIPTION",
				slog.String("text", transcript.Text),
				slog.Float64("confidence", transcript.Confidence))

			reply, err := asst.Respond(ctx, transcript.Text)
			var noMatch *match.NoMatchError
			switch {
			case errors.As(err, &noMatch):
				// TODO: fall back to the cloud LLM (#2)
				logger.Info("No command matched", slog.String("text", transcript.Text),
					slog.String("best_intent", noMatch.Best.Name),
					slog.Float64("best_score", noMatch.Best.Score))
				say(ctx, speaker, logger, app.NoMatchText)
			case err != nil:
				logger.Error("Command failed", slog.Any("error", err))
				say(ctx, speaker, logger, assistant.SpokenText(err))
			default:
				logger.Info("🗣️ RESPONSE",
					slog.String("intent", reply.Match.Name),
					slog.String("phrase", reply.Match.Phrase),
					slog.Float64("score", reply.Match.Score),
					slog.String("text", reply.Text))
				say(ctx, speaker, logger, reply.Text)
			}
		}
		logger.Info("Transcript channel closed")
	}()

	// Wait for termination signal
	<-sigChan

	// Cancel context to stop the services
	cancel()

	// Stop the speech-to-text service
	if err := sttService.Stop(); err != nil {
		// Silent shutdown
	}

	// Stop the microphone service
	if err := micService.Stop(); err != nil {
		// Silent shutdown
	}
}

func newSpeaker(engine string, logger *slog.Logger) (tts.Speaker, error) {
	switch engine {
	case "espeak":
		return espeak.New(espeak.Config{Logger: logger})
	case "none":
		return tts.NewLogSpeaker(logger), nil
	default:
		return nil, errors.WithMessagef(fault.ErrInvalidValue, "unknown -tts %q (want espeak or none)", engine)
	}
}

func say(ctx context.Context, speaker tts.Speaker, logger *slog.Logger, text string) {
	if err := speaker.Speak(ctx, text); err != nil && ctx.Err() == nil {
		logger.Error("Failed to speak response", slog.Any("error", err))
	}
}

func saveDir(save bool) string {
	if save {
		return ".recordings"
	}
	return ""
}
