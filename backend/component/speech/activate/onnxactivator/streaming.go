package onnxactivator

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"

	"github.com/sundbassen/alfred/component/speech"
	"github.com/sundbassen/alfred/internal/pcm"
)

// Streaming constants, ported from openWakeWord's AudioFeatures.
const (
	stepSamples      = 1280    // samples per scoring step (80 ms at 16 kHz)
	melContext       = 3 * 160 // trailing samples of earlier audio prepended to each step
	warmupSteps      = 5       // scores suppressed after start/reset (openWakeWord's prediction_buffer < 5 rule)
	noiseInitSamples = 4 * 16000
	noiseInitSeed    = 1
	statWindowSteps  = 62 // ~5 s of steps between score summary log lines
)

// streamingActivator ports openWakeWord's streaming feature extraction
// (AudioFeatures._streaming_features): audio is consumed in 1280-sample
// steps; each step adds 8 melspectrogram frames, one embedding of the last
// 76 frames, and one classifier score over the last 16 embeddings. The
// wake word is detected mid-utterance, without VAD gating.
//
// openWakeWord keeps 10 s of melspectrogram frames and 120 embeddings, but
// only the most recent 76 frames and 16 embeddings are ever read, so those
// are all this implementation retains.
//
// Not safe for concurrent use.
type streamingActivator struct {
	*sessions
	threshold  float32
	sampleRate int
	logger     *slog.Logger

	initialFeatures []float32 // embeddings of seeded noise, restored on Reset

	pending  []float32 // samples not yet forming a full step
	rawTail  []float32 // last melContext samples of processed audio
	melBuf   []float32 // last embeddingWindow frames, flat [frames * melBins]
	featBuf  []float32 // last classifierSteps embeddings, flat
	steps    int       // steps scored since start/reset
	statMax  float32
	statStep int

	scoreHook func(float32) // test hook, called with every step's score
}

// NewStreaming creates an activator that is fed continuous audio in chunks
// of any size. It reuses the same models and Config as New.
func NewStreaming(config Config) speech.StreamingActivator {
	s, err := loadSessions(config)
	if err != nil {
		config.Logger.Error("Failed to create streaming wake word activator", slog.Any("error", err))
		return nil
	}

	a := &streamingActivator{
		sessions:   s,
		threshold:  config.threshold(),
		sampleRate: config.SampleRate,
		logger:     config.Logger,
	}

	a.initialFeatures, err = a.noiseFeatures()
	if err != nil {
		config.Logger.Error("Failed to initialize wake word feature buffer", slog.Any("error", err))
		return nil
	}

	a.Reset()
	return a
}

// noiseFeatures returns the last classifierSteps embeddings of 4 s of
// random noise, matching openWakeWord's feature buffer initialization. A
// fixed seed keeps scores deterministic across runs.
func (a *streamingActivator) noiseFeatures() ([]float32, error) {
	rng := rand.New(rand.NewSource(noiseInitSeed))
	noise := make([]float32, noiseInitSamples)
	for i := range noise {
		noise[i] = float32(rng.Intn(2000) - 1000)
	}

	mel, err := a.runMelspec(noise)
	if err != nil {
		return nil, err
	}

	frames := len(mel) / melBins
	var batch []float32
	count := 0
	for i := 0; i+embeddingWindow <= frames; i += embeddingStep {
		batch = append(batch, mel[i*melBins:(i+embeddingWindow)*melBins]...)
		count++
	}
	if count < classifierSteps {
		return nil, fmt.Errorf("noise produced %d embeddings, need %d", count, classifierSteps)
	}

	emb, err := a.runEmbedding(batch, count)
	if err != nil {
		return nil, err
	}
	return emb[len(emb)-classifierSteps*embeddingDim:], nil
}

// Reset clears all buffered audio and features, so audio from before a
// detection cannot trigger again.
func (a *streamingActivator) Reset() {
	a.pending = a.pending[:0]
	a.rawTail = nil
	a.melBuf = make([]float32, embeddingWindow*melBins)
	for i := range a.melBuf {
		a.melBuf[i] = 1.0
	}
	a.featBuf = append([]float32(nil), a.initialFeatures...)
	a.steps = 0
}

// Feed consumes the next chunk of continuous PCM16 audio and reports
// whether the wake word was detected. On detection, any remaining samples
// of the chunk are discarded; callers are expected to Reset before feeding
// again.
func (a *streamingActivator) Feed(ctx context.Context, chunk []byte) (bool, error) {
	a.pending = append(a.pending, pcm.ToFloat32(chunk)...)

	consumed := 0
	defer func() {
		a.pending = append(a.pending[:0], a.pending[consumed:]...)
	}()

	for len(a.pending)-consumed >= stepSamples {
		step := a.pending[consumed : consumed+stepSamples]
		consumed += stepSamples

		score, err := a.processStep(step)
		if err != nil {
			return false, err
		}

		a.logStats(score)
		if a.scoreHook != nil {
			a.scoreHook(score)
		}

		if score >= a.threshold {
			a.logger.Info("Wake word detected",
				slog.Float64("score", float64(score)),
				slog.Float64("threshold", float64(a.threshold)))
			consumed = len(a.pending)
			return true, nil
		}
	}

	return false, nil
}

// processStep runs one 1280-sample step through the pipeline and returns
// its wake word score (0 during warmup).
func (a *streamingActivator) processStep(step []float32) (float32, error) {
	// Prepend trailing context so the melspectrogram's STFT windows are
	// continuous across steps: 480 + 1280 samples yield exactly 8 frames.
	input := append(append(make([]float32, 0, len(a.rawTail)+len(step)), a.rawTail...), step...)
	if len(step) >= melContext {
		a.rawTail = append(a.rawTail[:0], step[len(step)-melContext:]...)
	}

	frames, err := a.runMelspec(input)
	if err != nil {
		return 0, err
	}
	a.melBuf = append(a.melBuf, frames...)
	a.melBuf = a.melBuf[len(a.melBuf)-embeddingWindow*melBins:]

	emb, err := a.runEmbedding(a.melBuf, 1)
	if err != nil {
		return 0, err
	}
	a.featBuf = append(a.featBuf, emb...)
	a.featBuf = a.featBuf[len(a.featBuf)-classifierSteps*embeddingDim:]

	a.steps++
	if a.steps <= warmupSteps {
		return 0, nil
	}

	return a.runClassifier(a.featBuf)
}

// logStats logs the maximum score every ~5 s. This is the tuning signal for
// the threshold: compare max_score while speaking the wake word against
// max_score during ordinary speech and silence.
func (a *streamingActivator) logStats(score float32) {
	if score > a.statMax {
		a.statMax = score
	}
	a.statStep++
	if a.statStep < statWindowSteps {
		return
	}
	a.logger.Info("Wake word score stats",
		slog.Float64("max_score", float64(a.statMax)),
		slog.Float64("threshold", float64(a.threshold)))
	a.statMax = 0
	a.statStep = 0
}
