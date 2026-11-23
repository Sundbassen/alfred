package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sundbassen/alfred/component/command"
	"github.com/sundbassen/alfred/component/mic"
	"github.com/sundbassen/alfred/component/tts"
	"github.com/sundbassen/alfred/component/tts/espeak"
	"github.com/sundbassen/alfred/internal/logging"
	"github.com/sundbassen/alfred/internal/pipeline"
)

func main() {
	vad := flag.String("vad", "rms", "silence detector: rms (adaptive RMS) or silero (Silero VAD)")
	ttsEngine := flag.String("tts", "espeak", "response voice: espeak (espeak-ng) or none (log only)")
	save := flag.Bool("save", false,
		"save wake word clips, near misses and commands as wav files in .recordings (for training and debugging)")
	nearMiss := flag.Float64("near-miss", 0.005,
		"with -save, wake word score from which an undetected utterance is saved as a near miss")
	flag.Parse()

	// Create logger
	logger := logging.New()

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
	commands := pipeline.Commands()
	sttService, cleanup, err := pipeline.New(
		pipeline.Options{
			VAD:               *vad,
			Commands:          commands,
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

			resp, match, err := commands.Handle(ctx, transcript.Text)
			switch {
			case errors.Is(err, command.ErrNoMatch):
				// TODO: fall back to the cloud LLM (#2)
				attrs := []any{slog.String("text", transcript.Text)}
				if match.Command != nil {
					attrs = append(attrs,
						slog.String("best_command", match.Command.ID()),
						slog.Float64("best_score", match.Score))
				}
				logger.Info("No command matched", attrs...)
				say(ctx, speaker, logger, "Sorry, I didn't catch that.")
			case err != nil:
				logger.Error("Command failed", slog.Any("error", err))
			default:
				logger.Info("🗣️ RESPONSE",
					slog.String("command", match.Command.ID()),
					slog.String("phrase", match.Phrase),
					slog.Float64("score", match.Score),
					slog.String("text", resp.Text))
				say(ctx, speaker, logger, resp.Text)
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
		return nil, fmt.Errorf("unknown -tts %q (want espeak or none)", engine)
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
