// Package listener turns microphone audio into command transcripts: it
// feeds every chunk to the wake word detector and, after a detection,
// records the command until a silence gap and transcribes it (command.go).
package listener

import (
	"context"
	"log/slog"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/platform/pcm"
	"github.com/sundbassen/alfred/internal/voice"
	"gitlab.com/tozd/go/errors"
)

// Service defines the public interface for speech-to-text operations
type Service interface {
	// Start begins processing audio data for transcription
	Start(ctx context.Context) error

	// Stop stops the transcription service and cleans up resources
	Stop() error

	// ProcessAudio accepts audio data chunks for transcription
	ProcessAudio(audioData []byte) error

	// TranscriptChannel returns a channel that receives transcription results
	TranscriptChannel() <-chan voice.Transcript

	// Listen records the next command without the wake word, e.g. the
	// answer to a question Alfred just asked. If no speech starts within the
	// command timeout, it goes back to waiting for the wake word.
	Listen()
}

// service is the private implementation of Service
type service struct {
	transcriptChan  chan voice.Transcript
	audioChan       chan []byte
	listenChan      chan struct{}
	isRunning       bool
	language        string
	sampleRate      int
	silenceDetector voice.SilenceDetector
	transcriber     voice.Transcriber
	wakeWord        voice.WakeWordDetector
	command         commandConfig
	sink            AudioSink
	logger          *slog.Logger
}

// Config holds configuration for the speech-to-text service
type Config struct {
	Language        string                // Language code (e.g., "en", "es", "auto" for auto-detect)
	BufferSize      int                   // Internal buffer size for audio chunks
	SampleRate      int                   // Sample rate of input audio (must match microphone)
	SilenceDetector voice.SilenceDetector // Silence detection implementation
	Transcriber     voice.Transcriber     // Transcribes commands

	// WakeWord is fed every chunk; once it fires, the following
	// audio is recorded until a CommandSilenceMs gap and transcribed with
	// Transcriber (see command.go).
	WakeWord          voice.WakeWordDetector
	CommandSilenceMs  int // silence that ends a command (default 600)
	CommandTimeoutMs  int // give up if no speech starts this long after activation (default 4000)
	CommandMaxMs      int // force-end a command after this long (default 10000)
	CommandPreRollMs  int // audio kept from before command speech starts (default 100)
	CommandMinMs      int // minimum command length before a silence gap may end it (default 300)
	CommandLookbackMs int // audio from before a mid-speech activation prepended to the command (default 300)

	// Sink, if set, receives wake word clips, near misses and commands
	// (the -save option).
	Sink AudioSink
	// NearMissThreshold: listening utterances whose wake word score peaks
	// at or above this without a detection go to Sink.NearMiss
	// (default 0.005).
	NearMissThreshold float32
}

// DefaultConfig returns a default configuration
func DefaultConfig(silenceDetector voice.SilenceDetector, transcriber voice.Transcriber,
	wakeWord voice.WakeWordDetector,
) Config {
	return Config{
		Language:        "en",
		BufferSize:      100,
		SampleRate:      16000, // Must match microphone sample rate
		SilenceDetector: silenceDetector,
		Transcriber:     transcriber,
		WakeWord:        wakeWord,
	}
}

// New creates a new speech-to-text service instance
func New(config Config, logger *slog.Logger) (Service, error) {
	if config.Transcriber == nil {
		return nil, errors.WithMessage(fault.ErrInvalidValue, "transcriber is required")
	}
	if config.WakeWord == nil {
		return nil, errors.WithMessage(fault.ErrInvalidValue, "wake word detector is required")
	}
	if config.SampleRate == 0 {
		config.SampleRate = 16000
	}
	if config.NearMissThreshold == 0 {
		config.NearMissThreshold = 0.005
	}

	service := &service{
		transcriptChan:  make(chan voice.Transcript, config.BufferSize),
		audioChan:       make(chan []byte, config.BufferSize),
		listenChan:      make(chan struct{}, 1),
		isRunning:       false,
		language:        config.Language,
		sampleRate:      config.SampleRate,
		silenceDetector: config.SilenceDetector,
		transcriber:     config.Transcriber,
		wakeWord:        config.WakeWord,
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
func (s *service) Start(ctx context.Context) error {
	if s.isRunning {
		return errors.WithMessage(fault.ErrBadState, "listener is already running")
	}

	s.isRunning = true
	s.logger.Info("Speech-to-text service started")

	// Start the processing goroutine
	go s.commandLoop(ctx)

	return nil
}

// Stop stops the transcription service and cleans up resources
func (s *service) Stop() error {
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
func (s *service) ProcessAudio(audioData []byte) error {
	if !s.isRunning {
		return errors.WithMessage(fault.ErrBadState, "listener is not running")
	}

	select {
	case s.audioChan <- audioData:
		return nil
	default:
		s.logger.Warn("Audio buffer full, dropping frame")
		return errors.WithMessage(fault.ErrTemporary, "audio buffer full, dropping frame")
	}
}

// TranscriptChannel returns a channel that receives transcription results
func (s *service) TranscriptChannel() <-chan voice.Transcript {
	return s.transcriptChan
}

// Listen asks the command loop to record the next command without the wake
// word.
func (s *service) Listen() {
	select {
	case s.listenChan <- struct{}{}:
	default: // already requested
	}
}

func withDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// commandLoop feeds every chunk to the command controller (see command.go).
func (s *service) commandLoop(ctx context.Context) {
	controller := newCommandController(s.wakeWord, s.silenceDetector, s.transcriber,
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
		case <-s.listenChan:
			controller.followUp()
		}
	}
}

// emit sends a transcript without blocking if nobody is reading.
func (s *service) emit(transcript voice.Transcript) {
	select {
	case s.transcriptChan <- transcript:
	default:
		s.logger.Warn("voice.Transcript channel full, dropping transcript", slog.String("text", transcript.Text))
	}
}
