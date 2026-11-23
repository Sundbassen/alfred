// Package vad detects silence in audio chunks: an adaptive RMS detector
// here, Silero VAD in vad/silero.
package vad

import (
	"context"
	"log/slog"
	"math"

	"github.com/sundbassen/alfred/internal/voice"
)

// RMSConfig holds configuration for RMS-based silence detection
type RMSConfig struct {
	SilenceThreshold float32 // RMS threshold below which audio is considered silence (the minimum threshold when Adaptive)
	HighPassHz       float64 // High-pass cutoff applied before RMS; 0 disables. Suppresses low-frequency noise (footsteps, rumble) that is not speech
	SampleRate       int     // Sample rate of input audio (default 16000)
	Logger           *slog.Logger

	// Adaptive raises the threshold above SilenceThreshold when the ambient
	// noise floor rises: threshold = max(SilenceThreshold, floor*FloorRatio),
	// where floor is the lowest chunk RMS seen over the last FloorWindowMs.
	// Speech always has quieter moments between syllables, so the minimum
	// tracks the noise bed and is not dragged up by speech; a sustained
	// louder environment (fan, music) raises it within one window.
	Adaptive      bool
	FloorRatio    float64 // multiple of the noise floor that counts as speech (default 3)
	FloorWindowMs int     // noise floor tracking window (default 3000)
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

// statWindowMs is the audio duration between RMS statistics log lines. The
// stats exist to tune SilenceThreshold against the real microphone: ambient
// noise must fall below the threshold and speech above it.
const statWindowMs = 5000

// floorBlockMs is the granularity of the noise floor's sliding minimum.
const floorBlockMs = 250

// rmsSilenceDetector implements voice.SilenceDetector using RMS (Root Mean Square) analysis
type rmsSilenceDetector struct {
	threshold  float32
	highPass   *biquad // nil when disabled
	sampleRate int
	logger     *slog.Logger

	floor *noiseFloor // nil when not adaptive

	// rolling stats over the current log window
	statSamples int
	statCount   int
	statSum     float64
	statMin     float64
	statMax     float64
	statSilent  int
}

// NewRMSSilenceDetector creates a new RMS-based silence detector
func NewRMSSilenceDetector(config RMSConfig) voice.SilenceDetector {
	if config.SampleRate == 0 {
		config.SampleRate = 16000
	}

	var highPass *biquad
	if config.HighPassHz > 0 {
		highPass = newHighPassBiquad(config.HighPassHz, config.SampleRate)
	}

	var floor *noiseFloor
	if config.Adaptive {
		floor = newNoiseFloor(config)
	}

	return &rmsSilenceDetector{
		threshold:  config.SilenceThreshold,
		highPass:   highPass,
		sampleRate: config.SampleRate,
		logger:     config.Logger,
		floor:      floor,
	}
}

// effectiveThreshold returns the current RMS silence threshold.
func (r *rmsSilenceDetector) effectiveThreshold() float64 {
	threshold := float64(r.threshold)
	if r.floor != nil {
		threshold = max(threshold, r.floor.threshold())
	}
	return threshold
}

// IsSilent determines if an audio chunk is silence based on RMS level
func (r *rmsSilenceDetector) IsSilent(ctx context.Context, audioData []byte) bool {
	if len(audioData) == 0 {
		return true
	}

	rms := r.calculateRMS(audioData)
	samples := len(audioData) / 2
	if r.floor != nil {
		r.floor.add(rms, samples)
	}
	isSilent := rms < r.effectiveThreshold()

	r.logStats(rms, isSilent, samples)

	return isSilent
}

// logStats accumulates RMS statistics and logs a summary once per window.
// The summary is the tuning signal for SilenceThreshold: if silent_fraction
// stays at 0 the threshold is below the ambient noise floor (speech is never
// flushed for processing); if it stays at 1 the threshold is above speech
// level (nothing is ever recorded).
func (r *rmsSilenceDetector) logStats(rms float64, isSilent bool, samples int) {
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
	r.statSamples += samples
	if isSilent {
		r.statSilent++
	}

	if r.statSamples < statWindowMs*r.sampleRate/1000 {
		return
	}

	attrs := []any{
		slog.Float64("rms_avg", r.statSum/float64(r.statCount)),
		slog.Float64("rms_min", r.statMin),
		slog.Float64("rms_max", r.statMax),
		slog.Float64("silent_fraction", float64(r.statSilent)/float64(r.statCount)),
		slog.Float64("threshold", float64(r.threshold)),
	}
	if r.floor != nil {
		attrs = append(attrs,
			slog.Float64("noise_floor", r.floor.value()),
			slog.Float64("effective_threshold", r.effectiveThreshold()))
	}
	r.logger.Debug("Audio level stats", attrs...)

	r.statSamples = 0
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

// noiseFloor tracks the minimum chunk RMS over a sliding window, kept as a
// ring of per-block minimums so the window slides in floorBlockMs steps.
type noiseFloor struct {
	ratio        float64
	blockSamples int

	blocks     []float64 // minimum RMS of each completed block (ring)
	next       int       // ring index of the oldest block
	filled     int       // completed blocks in the ring
	current    float64   // minimum RMS of the block in progress
	curSamples int
}

func newNoiseFloor(config RMSConfig) *noiseFloor {
	ratio := config.FloorRatio
	if ratio == 0 {
		ratio = 3
	}
	windowMs := config.FloorWindowMs
	if windowMs == 0 {
		windowMs = 3000
	}
	return &noiseFloor{
		ratio:        ratio,
		blockSamples: floorBlockMs * config.SampleRate / 1000,
		blocks:       make([]float64, max(1, windowMs/floorBlockMs)),
		current:      math.Inf(1),
	}
}

func (f *noiseFloor) add(rms float64, samples int) {
	f.current = min(f.current, rms)
	f.curSamples += samples
	if f.curSamples < f.blockSamples {
		return
	}
	f.blocks[f.next] = f.current
	f.next = (f.next + 1) % len(f.blocks)
	f.filled = min(f.filled+1, len(f.blocks))
	f.current = math.Inf(1)
	f.curSamples = 0
}

// value returns the noise floor: the minimum RMS over the window, including
// the block in progress. Zero until any audio was seen.
func (f *noiseFloor) value() float64 {
	floor := f.current
	for i := 0; i < f.filled; i++ {
		floor = min(floor, f.blocks[i])
	}
	if math.IsInf(floor, 1) {
		return 0
	}
	return floor
}

func (f *noiseFloor) threshold() float64 {
	return f.value() * f.ratio
}
