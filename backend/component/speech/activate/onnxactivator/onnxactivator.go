package onnxactivator

import (
	"context"
	"log/slog"

	"github.com/sundbassen/alfred/component/speech"
	"github.com/sundbassen/alfred/internal/pcm"
)

// activator scores whole utterance buffers (used by -record mode). See
// streaming.go for the continuous variant used in the command phase.
type activator struct {
	*sessions
	threshold  float32
	sampleRate int
	logger     *slog.Logger
}

type Config struct {
	ModelPath          string       // Path to the trained wake word classifier (e.g., models/alfred/alfred.onnx)
	MelspecModelPath   string       // Path to openWakeWord's shared melspectrogram.onnx
	EmbeddingModelPath string       // Path to openWakeWord's shared embedding_model.onnx
	OnnxRuntimeLibPath string       // Path to libonnxruntime.so, loaded at runtime
	Threshold          float32      // Activation score threshold (default 0.5)
	SampleRate         int          // Sample rate of input audio (must match microphone)
	Logger             *slog.Logger // Logger instance
}

func (c Config) threshold() float32 {
	if c.Threshold == 0 {
		return 0.5
	}
	return c.Threshold
}

func New(config Config) speech.Activator {
	s, err := loadSessions(config)
	if err != nil {
		config.Logger.Error("Failed to create wake word activator", slog.Any("error", err))
		return nil
	}

	return &activator{
		sessions:   s,
		threshold:  config.threshold(),
		sampleRate: config.SampleRate,
		logger:     config.Logger,
	}
}

func (a *activator) Activate(ctx context.Context, data []byte) (bool, error) {
	score, err := a.score(data)
	if err != nil {
		return false, err
	}

	activated := score >= a.threshold

	a.logger.Info("Wake word score",
		slog.Float64("score", float64(score)),
		slog.Float64("threshold", float64(a.threshold)),
		slog.Float64("audio_seconds", pcm.BytesToSeconds(len(data), a.sampleRate)),
		slog.Bool("activated", activated))

	return activated, nil
}

// score runs the full three-model pipeline on raw PCM data and returns the
// maximum wake word probability found in the audio.
func (a *activator) score(data []byte) (float32, error) {
	samples := pcm.ToFloat32(data)

	// The melspectrogram model produces len/160 - 3 frames; anything shorter
	// than this yields no frames at all.
	if len(samples) < 4*160 {
		return 0, nil
	}

	melspec, err := a.computeMelspectrogram(samples)
	if err != nil {
		return 0, err
	}

	embeddings, err := a.computeEmbeddings(melspec)
	if err != nil {
		return 0, err
	}

	return a.classify(embeddings)
}

// computeMelspectrogram returns frames as a flat [frames * melBins] slice.
// The output starts with embeddingWindow rows of 1.0, matching
// openWakeWord's buffer initialization, so that short utterances still
// produce at least one embedding window.
func (a *activator) computeMelspectrogram(samples []float32) ([]float32, error) {
	frames, err := a.runMelspec(samples)
	if err != nil {
		return nil, err
	}

	melspec := make([]float32, embeddingWindow*melBins, embeddingWindow*melBins+len(frames))
	for i := range melspec {
		melspec[i] = 1.0
	}
	return append(melspec, frames...), nil
}

// computeEmbeddings slides a window over the melspectrogram frames, batches
// the windows, and runs the embedding model. Returns a flat
// [windows * embeddingDim] slice, left-padded by repeating the first
// embedding until there are at least classifierSteps of them.
func (a *activator) computeEmbeddings(melspec []float32) ([]float32, error) {
	frames := len(melspec) / melBins

	windowCount := 0
	var batch []float32
	for i := 0; i+embeddingWindow <= frames; i += embeddingStep {
		batch = append(batch, melspec[i*melBins:(i+embeddingWindow)*melBins]...)
		windowCount++
	}

	raw, err := a.runEmbedding(batch, windowCount)
	if err != nil {
		return nil, err
	}

	if windowCount >= classifierSteps {
		return raw, nil
	}

	// Left-pad with copies of the first embedding so the classifier always
	// has a full window.
	padded := make([]float32, 0, classifierSteps*embeddingDim)
	for i := 0; i < classifierSteps-windowCount; i++ {
		padded = append(padded, raw[:embeddingDim]...)
	}
	return append(padded, raw...), nil
}

// classify slides a window of classifierSteps embeddings over the sequence
// and returns the maximum wake word score across all windows.
func (a *activator) classify(embeddings []float32) (float32, error) {
	frames := len(embeddings) / embeddingDim

	var maxScore float32
	for i := 0; i+classifierSteps <= frames; i++ {
		score, err := a.runClassifier(embeddings[i*embeddingDim : (i+classifierSteps)*embeddingDim])
		if err != nil {
			return 0, err
		}
		if score > maxScore {
			maxScore = score
		}
	}

	return maxScore, nil
}
