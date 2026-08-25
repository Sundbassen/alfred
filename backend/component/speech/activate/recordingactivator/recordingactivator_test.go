package recordingactivator

import (
	"context"
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sundbassen/alfred/component/speech"
)

type fakeActivator struct {
	result bool
}

func (f *fakeActivator) Activate(ctx context.Context, data []byte) (bool, error) {
	return f.result, nil
}

func TestRecordsUtteranceAsWav(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	var service speech.Activator = New(Config{
		Inner:      &fakeActivator{result: true},
		Dir:        dir,
		SampleRate: 16000,
		Logger:     logger,
	})
	if service == nil {
		t.Fatal("New returned nil")
	}

	// 1 second of PCM16 audio
	data := make([]byte, 32000)
	for i := range data {
		data[i] = byte(i)
	}

	activated, err := service.Activate(context.Background(), data)
	if err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	if !activated {
		t.Error("expected delegation to inner activator result (true)")
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.wav"))
	if err != nil || len(files) != 1 {
		t.Fatalf("expected exactly 1 wav file, got %d (err %v)", len(files), err)
	}
	if !strings.Contains(files[0], "_hit.wav") {
		t.Errorf("expected activation outcome in filename, got %s", files[0])
	}

	wav, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(wav) != 44+len(data) {
		t.Fatalf("wav size = %d, want %d", len(wav), 44+len(data))
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		t.Error("missing RIFF/WAVE markers")
	}
	if rate := binary.LittleEndian.Uint32(wav[24:28]); rate != 16000 {
		t.Errorf("sample rate = %d, want 16000", rate)
	}
	if bits := binary.LittleEndian.Uint16(wav[34:36]); bits != 16 {
		t.Errorf("bits per sample = %d, want 16", bits)
	}
	if string(wav[44:52]) != string(data[:8]) {
		t.Error("PCM payload does not match input data")
	}
}
