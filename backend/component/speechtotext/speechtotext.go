package speechtotext

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/sundbassen/alfred/component/debounce"
	"github.com/sundbassen/alfred/component/speech"
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
	silenceDuration int
	silenceDetector debounce.SilenceDetector
	speechProcessor speech.SpeechProcessor
	logger          *slog.Logger
}

// Config holds configuration for the speech-to-text service
type Config struct {
	Language        string                   // Language code (e.g., "en", "es", "auto" for auto-detect)
	BufferSize      int                      // Internal buffer size for audio chunks
	SampleRate      int                      // Sample rate of input audio (must match microphone)
	SilenceDetector debounce.SilenceDetector // Silence detection implementation
	SilenceDuration int                      // Duration of silence in milliseconds before processing (e.g., 1000 for 1 second)
	SpeechProcessor speech.SpeechProcessor   // Speech processor for activation and command processing
}

// DefaultConfig returns a default configuration
func DefaultConfig(silenceDetector debounce.SilenceDetector, speechProcessor speech.SpeechProcessor) Config {
	return Config{
		Language:        "en",
		BufferSize:      100,
		SampleRate:      16000,           // Must match microphone sample rate
		SilenceDetector: silenceDetector, // Must be provided by caller
		SilenceDuration: 100,             // 100 milliseconds of silence triggers processing
		SpeechProcessor: speechProcessor, // Must be provided by caller
	}
}

// NewSTTService creates a new speech-to-text service instance
func NewSTTService(config Config, logger *slog.Logger) (SpeechToTextService, error) {
	if config.SpeechProcessor == nil {
		return nil, fmt.Errorf("speech processor is required")
	}

	service := &sttService{
		transcriptChan:  make(chan Transcript, config.BufferSize),
		audioChan:       make(chan []byte, config.BufferSize),
		isRunning:       false,
		language:        config.Language,
		sampleRate:      config.SampleRate,
		silenceDetector: config.SilenceDetector,
		silenceDuration: config.SilenceDuration,
		speechProcessor: config.SpeechProcessor,
		logger:          logger,
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
	go s.processAudioLoop(ctx)

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
		s.logger.Debug("Audio data queued for processing", slog.Int("bytes", len(audioData)))
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

// processAudioLoop processes incoming audio chunks and produces transcripts.
// Uses a debounce pattern: the utteranceAssembler accumulates speech (with
// pre-roll context) and emits a complete utterance once a silence gap
// follows it; each utterance is handed to the activator.
func (s *sttService) processAudioLoop(ctx context.Context) {
	// Assuming each chunk is ~30ms (480 samples at 16kHz)
	const chunkDurationMs = 30 // Adjust based on your microphone buffer size

	assembler := newUtteranceAssembler(assemblerConfig{
		preRollChunks:     8, // ~240ms of context before speech onset
		silenceGapChunks:  s.silenceDuration / chunkDurationMs,
		minUtteranceBytes: s.sampleRate * 2 * 1,  // 1 second minimum
		maxUtteranceBytes: s.sampleRate * 2 * 10, // force processing after 10 seconds without a silence gap
	})

	for {
		select {
		case <-ctx.Done():
			s.drainPending(ctx, assembler)
			return

		case audioData, ok := <-s.audioChan:
			if !ok {
				s.drainPending(ctx, assembler)
				return
			}

			isSilent := s.silenceDetector.IsSilent(ctx, audioData)

			utterance, reason := assembler.Add(audioData, isSilent)
			if reason == flushNone {
				continue
			}

			if reason == flushMaxDuration {
				// Safety valve: if no silence gap arrives (e.g. the silence
				// threshold sits below the ambient noise floor), process the
				// buffer anyway instead of accumulating it forever in silence.
				s.logger.Warn("No silence gap detected for 10s, forcing activation check; " +
					"the silence threshold is likely below the ambient noise level")
			}

			activated, err := s.speechProcessor.Activate(ctx, utterance)
			if err != nil {
				s.logger.Error("Activation check failed", slog.Any("error", err))
				// Don't return - continue processing
			} else if activated {
				s.logger.Info("System activated by speech processor")
			}
		}
	}
}

// drainPending processes any incomplete utterance left in the assembler at
// shutdown and emits its transcript.
func (s *sttService) drainPending(ctx context.Context, assembler *utteranceAssembler) {
	pending := assembler.Pending()
	if len(pending) < s.sampleRate*2 {
		return
	}

	result, err := s.speechProcessor.Process(ctx, pending)
	if err != nil {
		s.logger.Error("Failed to process remaining audio", slog.Any("error", err))
		return
	}

	transcript := Transcript{
		Text:       string(result.Text),
		Confidence: float64(result.Confidence),
		IsFinal:    true,
	}
	select {
	case s.transcriptChan <- transcript:
	default:
	}
}
