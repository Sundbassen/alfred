// Package wakeword detects the wake word with an openWakeWord model
// run through ONNX Runtime: melspectrogram -> embedding -> classifier. See
// streaming.go for the activator and sessions.go for the shared models.
package wakeword

import "log/slog"

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
