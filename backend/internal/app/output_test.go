package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// overlapSpeaker records whether two Speak calls ever ran at once.
type overlapSpeaker struct {
	active, overlaps atomic.Int32
}

func (s *overlapSpeaker) Speak(context.Context, string) error {
	if s.active.Add(1) > 1 {
		s.overlaps.Add(1)
	}
	time.Sleep(5 * time.Millisecond)
	s.active.Add(-1)
	return nil
}

func TestOutputSpeaksOneAtATime(t *testing.T) {
	speaker := &overlapSpeaker{}
	out := NewOutput(speaker, nil)
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			_ = out.Speak(context.Background(), "hello")
			_ = out.Chime(context.Background()) // no player: a no-op
		})
	}
	wg.Wait()
	if n := speaker.overlaps.Load(); n != 0 {
		t.Errorf("%d overlapping Speak calls", n)
	}
}
