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
	HighPassHz       float64 // High-pass cutoff applied before RMS; 0 disables. Suppresses low-frequency noise (footsteps, rumble) that is not speech
	SampleRate       int     // Sample rate of input audio, required when HighPassHz > 0
	Logger           *slog.Logger
}

// biquad is a 2nd-order IIR filter section (RBJ audio EQ cookbook form)
// with persistent state, so it can process a continuous stream chunk by
// chunk. Not safe for concurrent use; the detector is called from a single
// goroutine.
type biquad struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

// newHighPassBiquad creates a butterworth (Q=1/sqrt2) high-pass filter.
func newHighPassBiquad(cutoffHz float64, sampleRate int) *biquad {
	w0 := 2 * math.Pi * cutoffHz / float64(sampleRate)
	alpha := math.Sin(w0) / math.Sqrt2

	cosW0 := math.Cos(w0)
	a0 := 1 + alpha
	return &biquad{
		b0: (1 + cosW0) / 2 / a0,
		b1: -(1 + cosW0) / a0,
		b2: (1 + cosW0) / 2 / a0,
		a1: -2 * cosW0 / a0,
		a2: (1 - alpha) / a0,
	}
}

func (f *biquad) process(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y
	return y
}

// statWindowChunks is the number of chunks between RMS statistics log lines
// (~5 seconds at 30ms per chunk). The stats exist to tune SilenceThreshold
// against the real microphone: ambient noise must fall below the threshold
// and speech above it.
const statWindowChunks = 166

// rmsSilenceDetector implements SilenceDetector using RMS (Root Mean Square) analysis
type rmsSilenceDetector struct {
	threshold float32
	highPass  *biquad // nil when disabled
	logger    *slog.Logger

	// rolling stats over the current log window
	statCount  int
	statSum    float64
	statMin    float64
	statMax    float64
	statSilent int
}

// NewRMSSilenceDetector creates a new RMS-based silence detector
func NewRMSSilenceDetector(config RMSConfig) SilenceDetector {
	var highPass *biquad
	if config.HighPassHz > 0 {
		highPass = newHighPassBiquad(config.HighPassHz, config.SampleRate)
	}

	return &rmsSilenceDetector{
		threshold: config.SilenceThreshold,
		highPass:  highPass,
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

	r.logStats(rms, isSilent)

	return isSilent
}

// logStats accumulates RMS statistics and logs a summary once per window.
// The summary is the tuning signal for SilenceThreshold: if silent_fraction
// stays at 0 the threshold is below the ambient noise floor (speech is never
// flushed for processing); if it stays at 1 the threshold is above speech
// level (nothing is ever recorded).
func (r *rmsSilenceDetector) logStats(rms float64, isSilent bool) {
	if r.logger == nil {
		return
	}

	if r.statCount == 0 || rms < r.statMin {
		r.statMin = rms
	}
	if rms > r.statMax {
		r.statMax = rms
	}
	r.statSum += rms
	r.statCount++
	if isSilent {
		r.statSilent++
	}

	if r.statCount < statWindowChunks {
		return
	}

	r.logger.Info("Audio level stats",
		slog.Float64("rms_avg", r.statSum/float64(r.statCount)),
		slog.Float64("rms_min", r.statMin),
		slog.Float64("rms_max", r.statMax),
		slog.Float64("silent_fraction", float64(r.statSilent)/float64(r.statCount)),
		slog.Float64("threshold", float64(r.threshold)))

	r.statCount = 0
	r.statSum = 0
	r.statSilent = 0
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
		if r.highPass != nil {
			normalized = r.highPass.process(normalized)
		}
		sumSquares += normalized * normalized
	}

	return math.Sqrt(sumSquares / float64(sampleCount))
}
