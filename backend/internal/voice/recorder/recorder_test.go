package recorder

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sundbassen/alfred/internal/platform/wav"
	"github.com/sundbassen/alfred/internal/voice"
)

func TestRecorderWritesFiles(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir, 16000, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return time.Date(2026, 11, 23, 18, 50, 33, 368_000_000, time.Local) }
	r.started = r.now()

	audio := []byte{1, 0, 2, 0}
	r.WakeWord(audio, 0.0573)
	r.NearMiss(audio, 0.012)
	r.Command(audio, voice.Transcript{Text: "What time\tis it?", Confidence: 0.97})

	for _, path := range []string{
		"wakeword/20261123-185033.368_hit_0.057.wav",
		"wakeword/20261123-185033.368_near_0.012.wav",
		"commands/20261123-185033.368.wav",
	} {
		got, err := wav.Read(filepath.Join(dir, path), 16000)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if !bytes.Equal(got, audio) {
			t.Errorf("%s: audio = %v, want %v", path, got, audio)
		}
	}

	r.now = func() time.Time { return time.Date(2026, 11, 23, 19, 50, 33, 368_000_000, time.Local) }
	r.Close()
	sessions, err := os.ReadFile(filepath.Join(dir, "sessions.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	if fields := strings.Split(strings.TrimSpace(string(sessions)), "\t"); len(fields) != 3 || fields[2] != "3600.0" {
		t.Errorf("sessions.tsv = %q, want start, end, 3600.0", sessions)
	}

	index, err := os.ReadFile(filepath.Join(dir, "commands/index.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "20261123-185033.368.wav\t0.970\tWhat time is it?\n"; string(index) != want {
		t.Errorf("index.tsv = %q, want %q", index, want)
	}
}
