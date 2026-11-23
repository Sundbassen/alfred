package wakeword

import (
	"github.com/sundbassen/alfred/internal/platform/onnxrt"
	ort "github.com/yalue/onnxruntime_go"
	"gitlab.com/tozd/go/errors"
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

// sessions holds the three ONNX sessions shared by the batch and streaming
// activators, plus the single-model run helpers.
type sessions struct {
	melspec    *ort.DynamicAdvancedSession
	embedding  *ort.DynamicAdvancedSession
	classifier *ort.DynamicAdvancedSession
}

func loadSessions(config Config) (*sessions, error) {
	if err := onnxrt.Init(config.OnnxRuntimeLibPath); err != nil {
		return nil, errors.WithMessagef(err, "initialize ONNX runtime (%s)", config.OnnxRuntimeLibPath)
	}

	melspec, err := ort.NewDynamicAdvancedSession(
		config.MelspecModelPath, []string{"input"}, []string{"output"}, nil)
	if err != nil {
		return nil, errors.WithMessage(err, "load melspectrogram model")
	}

	embedding, err := ort.NewDynamicAdvancedSession(
		config.EmbeddingModelPath, []string{"input_1"}, []string{"conv2d_19"}, nil)
	if err != nil {
		melspec.Destroy()
		return nil, errors.WithMessage(err, "load embedding model")
	}

	classifier, err := ort.NewDynamicAdvancedSession(
		config.ModelPath, []string{"x"}, []string{"sigmoid"}, nil)
	if err != nil {
		melspec.Destroy()
		embedding.Destroy()
		return nil, errors.WithMessage(err, "load wake word model")
	}

	return &sessions{melspec: melspec, embedding: embedding, classifier: classifier}, nil
}

// runMelspec runs the melspectrogram model on raw (unnormalized) samples and
// returns len(samples)/160 - 3 frames as a flat [frames * melBins] slice,
// with openWakeWord's transform (v/10 + 2) applied to align the ONNX model
// with Google's original speech_embedding feature scaling.
func (s *sessions) runMelspec(samples []float32) ([]float32, error) {
	input, err := ort.NewTensor(ort.NewShape(1, int64(len(samples))), samples)
	if err != nil {
		return nil, err
	}
	defer input.Destroy()

	outputs := []ort.Value{nil}
	if err := s.melspec.Run([]ort.Value{input}, outputs); err != nil {
		return nil, err
	}
	output := outputs[0].(*ort.Tensor[float32])
	defer output.Destroy()

	// Model output shape is [1, 1, frames, melBins]; the data is already a
	// flat frames*melBins slice.
	raw := output.GetData()
	frames := make([]float32, len(raw))
	for i, v := range raw {
		frames[i] = v/10 + 2
	}
	return frames, nil
}

// runEmbedding runs the embedding model on a batch of count windows (flat
// [count * embeddingWindow * melBins]) and returns a flat
// [count * embeddingDim] slice.
func (s *sessions) runEmbedding(windows []float32, count int) ([]float32, error) {
	input, err := ort.NewTensor(
		ort.NewShape(int64(count), embeddingWindow, melBins, 1), windows)
	if err != nil {
		return nil, err
	}
	defer input.Destroy()

	outputs := []ort.Value{nil}
	if err := s.embedding.Run([]ort.Value{input}, outputs); err != nil {
		return nil, err
	}
	output := outputs[0].(*ort.Tensor[float32])
	defer output.Destroy()

	// Model output shape is [count, 1, 1, embeddingDim].
	return append([]float32(nil), output.GetData()...), nil
}

// runClassifier scores one window of classifierSteps embeddings (flat
// [classifierSteps * embeddingDim]).
func (s *sessions) runClassifier(window []float32) (float32, error) {
	input, err := ort.NewTensor(ort.NewShape(1, classifierSteps, embeddingDim), window)
	if err != nil {
		return 0, err
	}
	defer input.Destroy()

	outputs := []ort.Value{nil}
	if err := s.classifier.Run([]ort.Value{input}, outputs); err != nil {
		return 0, err
	}
	output := outputs[0].(*ort.Tensor[float32])
	defer output.Destroy()

	return output.GetData()[0], nil
}
