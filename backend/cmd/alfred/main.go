package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sundbassen/alfred/component/mic"
	"github.com/sundbassen/alfred/internal/logging"
	"github.com/sundbassen/alfred/internal/pipeline"
)

func main() {
	record := flag.Bool("record", false,
		"save every detected utterance as a wav file in .recordings (for collecting training samples)")
	vad := flag.String("vad", "rms", "silence detector: rms (adaptive RMS) or silero (Silero VAD)")
	flag.Parse()

	// Create logger
	logger := logging.New()

	// Create microphone service with default config
	config := mic.DefaultConfig()
	micService, err := mic.NewMicService(config, logger)
	if err != nil {
		logger.Error("Failed to create microphone service", slog.Any("error", err))
		os.Exit(1)
	}

	// Create the speech pipeline: streaming wake word detection followed by
	// command endpointing and transcription (or utterance recording with
	// -record)
	sttService, cleanup, err := pipeline.New(pipeline.Options{Record: *record, VAD: *vad}, logger)
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

	// Process transcriptions
	go func() {
		for transcript := range sttService.TranscriptChannel() {
			if transcript.Text != "" {
				logger.Info("📝 TRANSCRIPTION",
					slog.String("text", transcript.Text),
					slog.Float64("confidence", transcript.Confidence))
				// TODO: Handle transcription (e.g., send to another service, save to file, etc.)
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
