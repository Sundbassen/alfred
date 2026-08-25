package onnxactivator

import (
	"encoding/binary"
	"log/slog"
	"math"
	"os"
	"testing"
)

func TestActivatePipeline(t *testing.T) {
	paths := map[string]string{
		"model":     "../../../../models/alfred/hey_alfred.onnx",
		"melspec":   "../../../../models/openwakeword/melspectrogram.onnx",
		"embedding": "../../../../models/openwakeword/embedding_model.onnx",
		"onnxlib":   "/tmp/onnxruntime/lib/libonnxruntime.so",
	}
	for name, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("missing %s (%s); run download-model.sh and make install", name, path)
		}
	}

	service := New(Config{
		ModelPath:          paths["model"],
		MelspecModelPath:   paths["melspec"],
		EmbeddingModelPath: paths["embedding"],
		OnnxRuntimeLibPath: paths["onnxlib"],
		Threshold:          0.5,
		SampleRate:         16000,
		Logger:             slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	if service == nil {
		t.Fatal("New returned nil")
	}

	// 2 seconds of a 440 Hz sine at 16 kHz, amplitude 12000
	const sampleRate = 16000
	data := make([]byte, sampleRate*2*2)
	for i := 0; i < sampleRate*2; i++ {
		sample := int16(12000 * math.Sin(2*math.Pi*440*float64(i)/sampleRate))
		binary.LittleEndian.PutUint16(data[i*2:], uint16(sample))
	}

	activated, err := service.Activate(t.Context(), data)
	if err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	if activated {
		t.Error("sine wave should not activate the wake word")
	}

	// A pure tone is far outside the wake word distribution; any healthy
	// model/pipeline combination scores it near zero.
	score, err := service.(*activator).score(data)
	if err != nil {
		t.Fatalf("score failed: %v", err)
	}
	if score >= 0.1 {
		t.Errorf("score = %.8f for a sine wave, expected < 0.1", score)
	}
}
