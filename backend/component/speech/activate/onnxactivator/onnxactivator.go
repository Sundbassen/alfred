package onnxactivator

import (
	"context"
	"log/slog"
	"sync"

	"github.com/sundbassen/alfred/component/speech"
	ort "github.com/yalue/onnxruntime_go"
)

// The openWakeWord pipeline runs three models in sequence:
// raw PCM -> melspectrogram -> speech embeddings -> wake word classifier.
// These constants are fixed by the shared openWakeWord preprocessing models.
const (
	melBins         = 32 // mel bins produced per melspectrogram frame
	embeddingWindow = 76 // melspectrogram frames per embedding window
	embeddingStep   = 8  // melspectrogram frames between embedding windows
	embeddingDim    = 96 // features per embedding vector
	classifierSteps = 16 // embedding vectors per classifier input window
)

type activator struct {
	melspecSession    *ort.DynamicAdvancedSession
	embeddingSession  *ort.DynamicAdvancedSession
	classifierSession *ort.DynamicAdvancedSession
	threshold         float32
	sampleRate        int
	logger            *slog.Logger
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

// ONNX Runtime environment is process-global and must only be initialized once,
// even if multiple activators are created.
var (
	ortInitOnce sync.Once
	ortInitErr  error
)

func initRuntime(libPath string) error {
	ortInitOnce.Do(func() {
		ort.SetSharedLibraryPath(libPath)
		ortInitErr = ort.InitializeEnvironment()
	})
	return ortInitErr
}

func New(config Config) speech.Activator {
	if err := initRuntime(config.OnnxRuntimeLibPath); err != nil {
		config.Logger.Error("Failed to initialize ONNX runtime",
			slog.String("lib_path", config.OnnxRuntimeLibPath),
			slog.Any("error", err))
		return nil
	}

	melspecSession, err := ort.NewDynamicAdvancedSession(
		config.MelspecModelPath, []string{"input"}, []string{"output"}, nil)
	if err != nil {
		config.Logger.Error("Failed to load melspectrogram model", slog.Any("error", err))
		return nil
	}

	embeddingSession, err := ort.NewDynamicAdvancedSession(
		config.EmbeddingModelPath, []string{"input_1"}, []string{"conv2d_19"}, nil)
	if err != nil {
		config.Logger.Error("Failed to load embedding model", slog.Any("error", err))
		melspecSession.Destroy()
		return nil
	}

	classifierSession, err := ort.NewDynamicAdvancedSession(
		config.ModelPath, []string{"x"}, []string{"sigmoid"}, nil)
	if err != nil {
		config.Logger.Error("Failed to load wake word model", slog.Any("error", err))
		melspecSession.Destroy()
		embeddingSession.Destroy()
		return nil
	}

	threshold := config.Threshold
	if threshold == 0 {
		threshold = 0.5
	}

	return &activator{
		melspecSession:    melspecSession,
		embeddingSession:  embeddingSession,
		classifierSession: classifierSession,
		threshold:         threshold,
		sampleRate:        config.SampleRate,
		logger:            config.Logger,
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
		slog.Float64("audio_seconds", float64(len(data))/(float64(a.sampleRate)*2)),
		slog.Bool("activated", activated))

	return activated, nil
}

// score runs the full three-model pipeline on raw PCM data and returns the
// maximum wake word probability found in the audio.
func (a *activator) score(data []byte) (float32, error) {
	samples := a.bytesToFloat32(data)

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

// computeMelspectrogram runs the melspectrogram model on raw samples and
// returns frames as a flat [frames * melBins] slice. The output starts with
// embeddingWindow rows of 1.0, matching openWakeWord's buffer initialization,
// so that short utterances still produce at least one embedding window.
func (a *activator) computeMelspectrogram(samples []float32) ([]float32, error) {
	input, err := ort.NewTensor(ort.NewShape(1, int64(len(samples))), samples)
	if err != nil {
		return nil, err
	}
	defer input.Destroy()

	outputs := []ort.Value{nil}
	if err := a.melspecSession.Run([]ort.Value{input}, outputs); err != nil {
		return nil, err
	}
	output := outputs[0].(*ort.Tensor[float32])
	defer output.Destroy()

	// Model output shape is [1, 1, frames, melBins]; the data is already a
	// flat frames*melBins slice.
	raw := output.GetData()

	melspec := make([]float32, embeddingWindow*melBins+len(raw))
	for i := 0; i < embeddingWindow*melBins; i++ {
		melspec[i] = 1.0
	}
	// openWakeWord's transform to align the ONNX melspectrogram model with
	// Google's original speech_embedding feature scaling.
	for i, v := range raw {
		melspec[embeddingWindow*melBins+i] = v/10 + 2
	}

	return melspec, nil
}

// computeEmbeddings slides a window over the melspectrogram frames, batches
// the windows, and runs the embedding model. Returns a flat
// [windows * embeddingDim] slice, left-padded by repeating the first
// embedding until there are at least classifierSteps of them.
func (a *activator) computeEmbeddings(melspec []float32) ([]float32, error) {
	frames := len(melspec) / melBins

	windowCount := 0
	for i := 0; i+embeddingWindow <= frames; i += embeddingStep {
		windowCount++
	}

	batch := make([]float32, 0, windowCount*embeddingWindow*melBins)
	for i := 0; i+embeddingWindow <= frames; i += embeddingStep {
		batch = append(batch, melspec[i*melBins:(i+embeddingWindow)*melBins]...)
	}

	input, err := ort.NewTensor(
		ort.NewShape(int64(windowCount), embeddingWindow, melBins, 1), batch)
	if err != nil {
		return nil, err
	}
	defer input.Destroy()

	outputs := []ort.Value{nil}
	if err := a.embeddingSession.Run([]ort.Value{input}, outputs); err != nil {
		return nil, err
	}
	output := outputs[0].(*ort.Tensor[float32])
	defer output.Destroy()

	// Model output shape is [windows, 1, 1, embeddingDim]; the data is
	// already a flat windows*embeddingDim slice.
	raw := output.GetData()

	if windowCount >= classifierSteps {
		return append([]float32(nil), raw...), nil
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
		window := embeddings[i*embeddingDim : (i+classifierSteps)*embeddingDim]

		input, err := ort.NewTensor(ort.NewShape(1, classifierSteps, embeddingDim), window)
		if err != nil {
			return 0, err
		}

		outputs := []ort.Value{nil}
		if err := a.classifierSession.Run([]ort.Value{input}, outputs); err != nil {
			input.Destroy()
			return 0, err
		}
		output := outputs[0].(*ort.Tensor[float32])

		if score := output.GetData()[0]; score > maxScore {
			maxScore = score
		}

		output.Destroy()
		input.Destroy()
	}

	return maxScore, nil
}

// bytesToFloat32 converts little-endian int16 PCM data to float32 samples.
// The melspectrogram model expects raw int16 magnitudes cast to float32,
// without normalization to [-1.0, 1.0].
func (a *activator) bytesToFloat32(data []byte) []float32 {
	samples := make([]float32, len(data)/2)

	for i := 0; i < len(samples); i++ {
		sample := int16(data[i*2]) | int16(data[i*2+1])<<8
		samples[i] = float32(sample)
	}

	return samples
}
