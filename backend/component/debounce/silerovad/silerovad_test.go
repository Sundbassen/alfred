package silerovad

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	modelPath = "../../../models/silero/silero_vad.onnx"
	onnxLib   = "/tmp/onnxruntime/lib/libonnxruntime.so"
)

func newTestDetector(t *testing.T) *detector {
	t.Helper()
	for _, path := range []string{modelPath, onnxLib} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("missing %s; run download-model.sh and make install", path)
		}
	}
	d, err := New(Config{ModelPath: modelPath, OnnxRuntimeLibPath: onnxLib, SampleRate: 16000})
	if err != nil {
		t.Fatal(err)
	}
	return d.(*detector)
}

func TestSilenceIsSilent(t *testing.T) {
	d := newTestDetector(t)
	chunk := make([]byte, 480*2) // 30 ms of digital silence
	for i := 0; i < 100; i++ {
		if !d.IsSilent(t.Context(), chunk) {
			t.Fatalf("chunk %d of zeros classified as speech", i)
		}
	}
}

// TestRecordedSpeechIsDetected feeds validated wake word recordings (real
// speech) and expects at least some chunks to be classified as speech.
func TestRecordedSpeechIsDetected(t *testing.T) {
	files, _ := filepath.Glob("../../../.recordings/validated/*.wav")
	if len(files) == 0 {
		t.Skip("no validated recordings in .recordings/validated")
	}
	d := newTestDetector(t)

	for _, file := range files[:min(5, len(files))] {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		pcmData := data[44:] // 16 kHz mono s16 wav written by recordingactivator

		speech := 0
		for off := 0; off+960 <= len(pcmData); off += 960 {
			if !d.IsSilent(t.Context(), pcmData[off:off+960]) {
				speech++
			}
		}
		if speech == 0 {
			t.Errorf("%s: no chunk classified as speech", filepath.Base(file))
		}
	}
}
