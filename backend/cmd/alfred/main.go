package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sundbassen/alfred/component/debounce"
	"github.com/sundbassen/alfred/component/mic"
	"github.com/sundbassen/alfred/component/speech"
	"github.com/sundbassen/alfred/component/speech/activate/onnxactivator"
	"github.com/sundbassen/alfred/component/speech/activate/recordingactivator"
	"github.com/sundbassen/alfred/component/speechtotext"
	"github.com/sundbassen/alfred/internal/logging"
)

func main() {
	record := flag.Bool("record", false,
		"save every detected utterance as a wav file in .recordings (for collecting training samples)")
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

	// Create wake word activator using the trained openWakeWord model
	activatorConfig := onnxactivator.Config{
		ModelPath:          "models/alfred/hey_alfred.onnx",
		MelspecModelPath:   "models/openwakeword/melspectrogram.onnx",
		EmbeddingModelPath: "models/openwakeword/embedding_model.onnx",
		OnnxRuntimeLibPath: "/tmp/onnxruntime/lib/libonnxruntime.so",
		Threshold:          0.03,
		SampleRate:         16000,
		Logger:             logger,
	}
	activatorService := onnxactivator.New(activatorConfig)
	if activatorService == nil {
		logger.Error("Failed to create wake word activator")
		os.Exit(1)
	}

	// In record mode, wrap the activator so every scored utterance is saved
	// as a wav file for wake word training (--custom-samples)
	if *record {
		activatorService = recordingactivator.New(recordingactivator.Config{
			Inner:      activatorService,
			Dir:        ".recordings",
			SampleRate: 16000,
			Logger:     logger,
		})
		if activatorService == nil {
			logger.Error("Failed to create recording activator")
			os.Exit(1)
		}
	}

	// Create placeholder processor
	placeholderProcessor := speech.NewPlaceholderProcessor()

	// Create speech processor combining processor and activator
	speechProcessor := speech.NewSpeechProcessor(placeholderProcessor, activatorService)

	// Create speech-to-text service
	sttConfig := speechtotext.DefaultConfig(
		debounce.NewRMSSilenceDetector(
			debounce.RMSConfig{
				// RMS of normalized samples. Tune with the "Audio level
				// stats" log: must sit above ambient rms_avg and below
				// speech rms_max. Measured 2026-07-19 with mic source at
				// 25% volume: ambient ~0.004.
				SilenceThreshold: 0.015,
				// Footsteps and rumble are low-frequency; speech is not.
				// The high-pass keeps walking noise from counting as speech.
				HighPassHz: 200,
				SampleRate: 16000,
				Logger:     logger,
			},
		),
		speechProcessor,
	)
	sttConfig.SilenceDuration = 500 // 0.5 second pause triggers processing
	sttService, err := speechtotext.NewSTTService(sttConfig, logger)
	if err != nil {
		logger.Error("Failed to create speech-to-text service", slog.Any("error", err))
		os.Exit(1)
	}
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
