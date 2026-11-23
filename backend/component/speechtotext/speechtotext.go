package speechtotext

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/sundbassen/alfred/component/debounce"
	"github.com/sundbassen/alfred/component/speech"
	"github.com/sundbassen/alfred/internal/pcm"
)

// Transcript represents a transcription result
type Transcript struct {
	Text       string  // The transcribed text
	Confidence float64 // Confidence score (0.0 to 1.0)
	IsFinal    bool    // Whether this is a final result or partial
}

// SpeechToTextService defines the public interface for speech-to-text operations
type SpeechToTextService interface {
	// Start begins processing audio data for transcription
	Start(ctx context.Context) error

	// Stop stops the transcription service and cleans up resources
	Stop() error

	// ProcessAudio accepts audio data chunks for transcription
	ProcessAudio(audioData []byte) error

	// TranscriptChannel returns a channel that receives transcription results
	TranscriptChannel() <-chan Transcript
}

// sttService is the private implementation of SpeechToTextService
type sttService struct {
	transcriptChan  chan Transcript
	audioChan       chan []byte
	isRunning       bool
	language        string
	sampleRate      int
	silenceDetector debounce.SilenceDetector
	processor       speech.Processor
	streaming       speech.StreamingActivator
	command         commandConfig
	sink            AudioSink
	logger          *slog.Logger
}

// Config holds configuration for the speech-to-text service
type Config struct {
	Language        string                   // Language code (e.g., "en", "es", "auto" for auto-detect)
	BufferSize      int                      // Internal buffer size for audio chunks
	SampleRate      int                      // Sample rate of input audio (must match microphone)
	SilenceDetector debounce.SilenceDetector // Silence detection implementation
	Processor       speech.Processor         // Transcribes commands

	// StreamingActivator is fed every chunk; once it fires, the following
	// audio is recorded until a CommandSilenceMs gap and transcribed with
	// Processor (see command.go).
	StreamingActivator speech.StreamingActivator
	CommandSilenceMs   int // silence that ends a command (default 600)
	CommandTimeoutMs   int // give up if no speech starts this long after activation (default 4000)
	CommandMaxMs       int // force-end a command after this long (default 10000)
	CommandPreRollMs   int // audio kept from before command speech starts (default 100)
	CommandMinMs       int // minimum command length before a silence gap may end it (default 300)
	CommandLookbackMs  int // audio from before a mid-speech activation prepended to the command (default 300)

	// Sink, if set, receives wake word clips, near misses and commands
	// (the -save option).
	Sink AudioSink
	// NearMissThreshold: listening utterances whose wake word score peaks
	// at or above this without a detection go to Sink.NearMiss
	// (default 0.005).
	NearMissThreshold float32
}

// DefaultConfig returns a default configuration
func DefaultConfig(silenceDetector debounce.SilenceDetector, processor speech.Processor,
	activator speech.StreamingActivator,
) Config {
	return Config{
		Language:           "en",
		BufferSize:         100,
		SampleRate:         16000, // Must match microphone sample rate
		SilenceDetector:    silenceDetector,
		Processor:          processor,
		StreamingActivator: activator,
	}
}

// NewSTTService creates a new speech-to-text service instance
func NewSTTService(config Config, logger *slog.Logger) (SpeechToTextService, error) {
	if config.Processor == nil {
		return nil, fmt.Errorf("processor is required")
	}
	if config.StreamingActivator == nil {
		return nil, fmt.Errorf("streaming activator is required")
	}
	if config.SampleRate == 0 {
		config.SampleRate = 16000
	}
	if config.NearMissThreshold == 0 {
		config.NearMissThreshold = 0.005
	}

	service := &sttService{
		transcriptChan:  make(chan Transcript, config.BufferSize),
		audioChan:       make(chan []byte, config.BufferSize),
		isRunning:       false,
		language:        config.Language,
		sampleRate:      config.SampleRate,
		silenceDetector: config.SilenceDetector,
		processor:       config.Processor,
		streaming:       config.StreamingActivator,
		command: commandConfig{
			sampleRate:      config.SampleRate,
			silenceGapBytes: pcm.MsToBytes(withDefault(config.CommandSilenceMs, 600), config.SampleRate),
			timeoutBytes:    pcm.MsToBytes(withDefault(config.CommandTimeoutMs, 4000), config.SampleRate),
			maxBytes:        pcm.MsToBytes(withDefault(config.CommandMaxMs, 10000), config.SampleRate),
			preRollBytes:    pcm.MsToBytes(withDefault(config.CommandPreRollMs, 100), config.SampleRate),
			minBytes:        pcm.MsToBytes(withDefault(config.CommandMinMs, 300), config.SampleRate),

			lookbackBytes:      pcm.MsToBytes(withDefault(config.CommandLookbackMs, 300), config.SampleRate),
			listenPreRollBytes: pcm.MsToBytes(300, config.SampleRate),
			hitClipBytes:       pcm.MsToBytes(3000, config.SampleRate),
			nearMissThreshold:  config.NearMissThreshold,
		},
		sink:   config.Sink,
		logger: logger,
	}

	return service, nil
}

// Start begins processing audio data for transcription
func (s *sttService) Start(ctx context.Context) error {
	if s.isRunning {
		return fmt.Errorf("speech-to-text service is already running")
	}

	s.isRunning = true
	s.logger.Info("Speech-to-text service started")

	// Start the processing goroutine
	go s.commandLoop(ctx)

	return nil
}

// Stop stops the transcription service and cleans up resources
func (s *sttService) Stop() error {
	if !s.isRunning {
		return nil
	}

	s.isRunning = false

	// Channels are intentionally not closed: processAudioLoop may still be
	// draining and sending on them (closing here caused a "send on closed
	// channel" panic at shutdown). The loop exits via context cancellation
	// and the channels are reclaimed with the process.
	s.logger.Info("Speech-to-text service stopped")
	return nil
}

// ProcessAudio accepts audio data chunks for transcription
func (s *sttService) ProcessAudio(audioData []byte) error {
	if !s.isRunning {
		return fmt.Errorf("service is not running")
	}

	select {
	case s.audioChan <- audioData:
		return nil
	default:
		s.logger.Warn("Audio buffer full, dropping frame")
		return fmt.Errorf("audio buffer full, dropping frame")
	}
}

// TranscriptChannel returns a channel that receives transcription results
func (s *sttService) TranscriptChannel() <-chan Transcript {
	return s.transcriptChan
}

func withDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// commandLoop feeds every chunk to the command controller (see command.go).
func (s *sttService) commandLoop(ctx context.Context) {
	controller := newCommandController(s.streaming, s.silenceDetector, s.processor,
		s.sink, s.command, s.logger, s.emit)

	for {
		select {
		case <-ctx.Done():
			controller.drain(context.WithoutCancel(ctx))
			return
		case audioData, ok := <-s.audioChan:
			if !ok {
				controller.drain(ctx)
				return
			}
			controller.handle(ctx, audioData)
		}
	}
}

// emit sends a transcript without blocking if nobody is reading.
func (s *sttService) emit(transcript Transcript) {
	select {
	case s.transcriptChan <- transcript:
	default:
		s.logger.Warn("Transcript channel full, dropping transcript", slog.String("text", transcript.Text))
	}
}
