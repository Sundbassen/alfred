package wakeword

import (
	"encoding/binary"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/sundbassen/alfred/internal/platform/onnxrt"
	"github.com/sundbassen/alfred/internal/platform/wav"
)

func testConfig(t *testing.T, threshold float32) Config {
	t.Helper()
	paths := map[string]string{
		"model":     "../../../models/alfred/hey_alfred.onnx",
		"melspec":   "../../../models/openwakeword/melspectrogram.onnx",
		"embedding": "../../../models/openwakeword/embedding_model.onnx",
		"onnxlib":   onnxrt.LibPath(), // set by make test
	}
	for name, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("missing %s (%s); run download-model.sh and make install", name, path)
		}
	}
	return Config{
		ModelPath:          paths["model"],
		MelspecModelPath:   paths["melspec"],
		EmbeddingModelPath: paths["embedding"],
		OnnxRuntimeLibPath: paths["onnxlib"],
		Threshold:          threshold,
		SampleRate:         16000,
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func newStreaming(t *testing.T, threshold float32) *streamingActivator {
	t.Helper()
	a := NewStreaming(testConfig(t, threshold))
	if a == nil {
		t.Fatal("NewStreaming returned nil")
	}
	return a.(*streamingActivator)
}

func sine(seconds float64) []byte {
	const sampleRate = 16000
	n := int(seconds * sampleRate)
	data := make([]byte, n*2)
	for i := 0; i < n; i++ {
		s := int16(12000 * math.Sin(2*math.Pi*440*float64(i)/sampleRate))
		binary.LittleEndian.PutUint16(data[i*2:], uint16(s))
	}
	return data
}

// feedScores feeds data in chunks of chunkBytes and returns every step's
// score.
func feedScores(t *testing.T, a *streamingActivator, data []byte, chunkBytes int) []float32 {
	t.Helper()
	var scores []float32
	a.scoreHook = func(s float32) { scores = append(scores, s) }
	defer func() { a.scoreHook = nil }()
	for off := 0; off < len(data); off += chunkBytes {
		if _, _, err := a.Feed(t.Context(), data[off:min(off+chunkBytes, len(data))]); err != nil {
			t.Fatal(err)
		}
	}
	return scores
}

func TestStreamingSineDoesNotActivate(t *testing.T) {
	a := newStreaming(t, 0.5)
	data := sine(3)
	for off := 0; off < len(data); off += 960 {
		_, activated, err := a.Feed(t.Context(), data[off:min(off+960, len(data))])
		if err != nil {
			t.Fatal(err)
		}
		if activated {
			t.Fatal("sine wave activated the wake word")
		}
	}
}

// TestStreamingChunkSizeInvariance: the score sequence depends only on the
// audio, not on how it is chunked. This exercises the remainder and
// melspectrogram context handling.
func TestStreamingChunkSizeInvariance(t *testing.T) {
	a := newStreaming(t, 2) // never fires, so every step is scored
	data := wakeWordAudio(t)

	reference := feedScores(t, a, data, 960) // 480-sample mic chunks
	if want := len(data) / 2 / stepSamples; len(reference) != want {
		t.Fatalf("got %d scores, want %d (one per %d samples)", len(reference), want, stepSamples)
	}

	for _, chunkBytes := range []int{2000, 2, 2*stepSamples + 2, 666} {
		a.Reset()
		got := feedScores(t, a, data, chunkBytes)
		if len(got) != len(reference) {
			t.Fatalf("chunk %d bytes: %d scores, want %d", chunkBytes, len(got), len(reference))
		}
		for i := range got {
			if math.Abs(float64(got[i]-reference[i])) > 1e-5 {
				t.Fatalf("chunk %d bytes: score[%d] = %g, want %g", chunkBytes, i, got[i], reference[i])
			}
		}
	}
}

// TestStreamingDetectsValidatedRecordings streams real wake word
// recordings (reviewed hits in .recordings/validated) and checks that each
// one scores at least half the detection threshold. The margin is the one
// the former batch-vs-streaming comparison allowed: many of these files
// were labeled "hit" by the removed batch scorer, which scores whole
// utterances slightly differently. Recordings below the threshold itself
// are logged; they are real misses for the live system (see make
// wakestats).
func TestStreamingDetectsValidatedRecordings(t *testing.T) {
	files, _ := filepath.Glob("../../../.recordings/validated/*_hit*.wav")
	if len(files) == 0 {
		t.Skip("no validated hit recordings in .recordings/validated")
	}
	const threshold = 0.03
	stream := newStreaming(t, 2)

	for _, file := range files {
		// Silence on both sides: the streaming path needs warmup steps
		// before it scores, and room after the word for the score to peak.
		data := readWav(t, file)
		padded := append(append(make([]byte, 16000), data...), make([]byte, 16000)...)

		stream.Reset()
		var streamMax float32
		for _, s := range feedScores(t, stream, padded, 960) {
			streamMax = max(streamMax, s)
		}

		t.Logf("%s: streaming max %.4f", filepath.Base(file), streamMax)
		switch {
		case streamMax < threshold*0.5:
			t.Errorf("%s: validated wake word scores only %.4f streaming (threshold %.2f)",
				filepath.Base(file), streamMax, threshold)
		case streamMax < threshold:
			t.Logf("%s: below the threshold, would be missed live", filepath.Base(file))
		}
	}
}

// wakeWordAudio returns a validated recording when available (to exercise
// realistic, non-trivial scores), otherwise a sine wave.
func wakeWordAudio(t *testing.T) []byte {
	files, _ := filepath.Glob("../../../.recordings/validated/*.wav")
	if len(files) > 0 {
		return readWav(t, files[0])
	}
	return sine(3)
}

// readWav returns the PCM data of a 16 kHz mono s16 wav file.
func readWav(t *testing.T, path string) []byte {
	t.Helper()
	data, err := wav.Read(path, 16000)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
