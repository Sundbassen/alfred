package espeak

import (
	"bytes"
	"context"
	"testing"
)

func TestSynthesize(t *testing.T) {
	e, err := New(Config{})
	if err != nil {
		t.Skip("espeak-ng not installed")
	}
	wav, err := e.Synthesize(context.Background(), "Timer set for ten minutes.")
	if err != nil {
		t.Fatal(err)
	}
	if len(wav) < 1000 || !bytes.HasPrefix(wav, []byte("RIFF")) || !bytes.Equal(wav[8:12], []byte("WAVE")) {
		t.Errorf("not a WAV file: %d bytes, starts %q", len(wav), wav[:min(12, len(wav))])
	}
}
