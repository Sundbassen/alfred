package sound

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/sundbassen/alfred/internal/platform/wav"
)

func TestChimeIsWav(t *testing.T) {
	pcm, err := wav.Decode(Chime(), chimeRate)
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(len(pcm)/2) * time.Second / chimeRate; got < 500*time.Millisecond || got > 2*time.Second {
		t.Errorf("chime lasts %s", got)
	}
}

func TestPlayFailure(t *testing.T) {
	path, err := exec.LookPath("false")
	if err != nil {
		t.Skip("no false binary")
	}
	if err := (&Player{path: path}).Play(context.Background(), Chime()); err == nil {
		t.Error("Play with a failing player returned nil")
	}
}
