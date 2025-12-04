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
	close(s.audioChan)
	close(s.transcriptChan)

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

// processAudioLoop processes incoming audio chunks and produces transcripts
// Uses a debounce pattern: accumulates speech and waits for silence before processing
func (s *sttService) processAudioLoop(ctx context.Context) {
	var audioBuffer []byte
	var silenceCounter int
	const minSpeechDuration = 16000 * 2 * 1 // 1 second minimum (16kHz * 2 bytes * 1 sec)

	// Calculate how many chunks equal the silence duration
	// Assuming each chunk is ~30ms (480 samples at 16kHz)
	chunkDurationMs := 30 // Adjust based on your microphone buffer size
	silenceChunksNeeded := s.silenceDuration / chunkDurationMs

	for {
		select {
		case <-ctx.Done():
			// Process any remaining audio before exiting
			if len(audioBuffer) >= minSpeechDuration {
				result, err := s.speechProcessor.Process(ctx, audioBuffer)
				if err != nil {
					s.logger.Error("Failed to process remaining audio", slog.Any("error", err))
				} else {
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
			}
			return

		case audioData, ok := <-s.audioChan:
			if !ok {
				// Process any remaining audio before exiting
				if len(audioBuffer) >= minSpeechDuration {
					result, err := s.speechProcessor.Process(ctx, audioBuffer)
					if err != nil {
						s.logger.Error("Failed to process remaining audio", slog.Any("error", err))
					} else {
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
				}
				return
			}

			// Check if this chunk is silence
			isSilent := s.silenceDetector.IsSilent(ctx, audioData)

			// Debug logging to track silence detection
			s.logger.Debug("Audio chunk analysis",
				slog.Bool("is_silent", isSilent),
				slog.Int("silence_counter", silenceCounter),
				slog.Int("buffer_size", len(audioBuffer)),
				slog.Int("silence_needed", silenceChunksNeeded),
			)

			if isSilent {
				silenceCounter++

				// If we've been silent long enough and have speech buffered
				if silenceCounter >= silenceChunksNeeded && len(audioBuffer) >= minSpeechDuration {
					// Send accumulated audio buffer to activator for activation checking
					activated, err := s.speechProcessor.Activate(ctx, audioBuffer)
					if err != nil {
						s.logger.Error("Activation check failed", slog.Any("error", err))
						// Don't return - continue processing
					} else if activated {
						s.logger.Info("System activated by speech processor")
					}

					// Process the accumulated speech
					// s.logger.Info("Processing speech segment",
					// 	slog.Int("buffer_bytes", len(audioBuffer)),
					// 	slog.Float64("duration_seconds", float64(len(audioBuffer))/(float64(s.sampleRate)*2.0)))

					// result, err := s.speechProcessor.Process(ctx, audioBuffer)
					// if err != nil {
					// 	s.logger.Error("Failed to process speech segment", slog.Any("error", err))
					// } else {
					// 	transcript := Transcript{
					// 		Text:       string(result.Text),
					// 		Confidence: float64(result.Confidence),
					// 		IsFinal:    true,
					// 	}

					// 	select {
					// 	case s.transcriptChan <- transcript:
					// 		s.logger.Info("Transcript sent", slog.String("text", transcript.Text))
					// 	case <-ctx.Done():
					// 		return
					// 	default:
					// 		// Transcript channel full, skip
					// 		s.logger.Warn("Transcript channel full, dropping transcript")
					// 	}
					// }

					// Reset for next speech segment
					audioBuffer = nil
					silenceCounter = 0
				}
			} else {
				// Speech detected - reset silence counter and accumulate
				if silenceCounter > 0 {
					s.logger.Debug("Speech detected, resetting silence counter",
						slog.Int("previous_silence_count", silenceCounter),
					)
				}
				silenceCounter = 0
				audioBuffer = append(audioBuffer, audioData...)
			}
		}
	}
}
