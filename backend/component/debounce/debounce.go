package debounce

import (
	"context"
	"log/slog"
	"math"
)

// SilenceDetector defines an interface for detecting silence in audio data
type SilenceDetector interface {
	// IsSilent determines if the given audio data represents silence
	IsSilent(ctx context.Context, audioData []byte) bool
}

// RMSConfig holds configuration for RMS-based silence detection
type RMSConfig struct {
	SilenceThreshold float32 // RMS threshold below which audio is considered silence
	Logger           *slog.Logger
}

// rmsSilenceDetector implements SilenceDetector using RMS (Root Mean Square) analysis
type rmsSilenceDetector struct {
	threshold float32
	logger    *slog.Logger
}

// NewRMSSilenceDetector creates a new RMS-based silence detector
func NewRMSSilenceDetector(config RMSConfig) SilenceDetector {
	return &rmsSilenceDetector{
		threshold: config.SilenceThreshold,
		logger:    config.Logger,
	}
}

// IsSilent determines if an audio chunk is silence based on RMS level
func (r *rmsSilenceDetector) IsSilent(ctx context.Context, audioData []byte) bool {
	if len(audioData) == 0 {
		return true
	}

	rms := r.calculateRMS(audioData)
	isSilent := rms < float64(r.threshold)

	// Log RMS values periodically to help with threshold tuning
	if r.logger != nil {
		r.logger.Debug("RMS analysis",
			slog.Float64("rms", rms),
			slog.Float64("threshold", float64(r.threshold)),
			slog.Bool("is_silent", isSilent))
	}

	return isSilent
}

// calculateRMS calculates the Root Mean Square (volume level) of an audio chunk
func (r *rmsSilenceDetector) calculateRMS(audioData []byte) float64 {
	if len(audioData) == 0 {
		return 0
	}

	var sumSquares float64
	sampleCount := len(audioData) / 2

	for i := 0; i < sampleCount; i++ {
		// Convert little-endian int16 to sample value
		sample := int16(audioData[i*2]) | int16(audioData[i*2+1])<<8
		normalized := float64(sample) / float64(math.MaxInt16)
		sumSquares += normalized * normalized
	}

	return math.Sqrt(sumSquares / float64(sampleCount))
}
