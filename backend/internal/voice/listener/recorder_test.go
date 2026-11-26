package listener

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func newTestRecorder(t *testing.T) (*Recorder, *fakeProcessor) {
	t.Helper()
	proc := &fakeProcessor{}
	// 4-byte chunks at a sample rate that makes 1 ms = 2 bytes: the gap is
	// 8 bytes, the timeout 12, the minimum 8 (as in testCommandConfig).
	r, err := NewRecorder(RecorderConfig{SampleRate: 1000, SilenceDetector: &fakeDetector{}, Transcriber: proc,
		SilenceMs: 4, TimeoutMs: 6, MaxMs: 20, MinMs: 4}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return r, proc
}

func TestRecorderTranscribesACommand(t *testing.T) {
	r, proc := newTestRecorder(t)
	ctx := context.Background()
	r.Start()
	for _, chunk := range [][]byte{c4('V', 1), c4('V', 2), c4('V', 3), c4('S', 4)} {
		if _, done := r.Feed(ctx, chunk); done {
			t.Fatal("done before the silence gap")
		}
	}
	transcript, done := r.Feed(ctx, c4('S', 5))
	if !done || transcript == nil || transcript.Text != "ok" || len(proc.got) != 1 {
		t.Fatalf("after the gap: %+v, %v (transcribed %d)", transcript, done, len(proc.got))
	}
	if _, done := r.Feed(ctx, c4('V', 6)); !done {
		t.Error("chunks after the end should report done until the next Start")
	}

	// Again, for the next command.
	r.Start()
	r.Feed(ctx, c4('V', 7))
	r.Feed(ctx, c4('V', 8))
	r.Feed(ctx, c4('S', 9))
	if transcript, done := r.Feed(ctx, c4('S', 10)); !done || transcript == nil || len(proc.got) != 2 {
		t.Errorf("second command: %+v, %v", transcript, done)
	}
}

func TestRecorderNoSpeech(t *testing.T) {
	r, proc := newTestRecorder(t)
	ctx := context.Background()
	r.Start()
	for i := range 5 {
		transcript, done := r.Feed(ctx, c4('S', byte(i)))
		if done {
			if transcript != nil || len(proc.got) != 0 {
				t.Errorf("silence only: transcript %+v, transcribed %d", transcript, len(proc.got))
			}
			return
		}
	}
	t.Error("silence only never timed out")
}

func TestRecorderFinishEarly(t *testing.T) {
	r, proc := newTestRecorder(t)
	ctx := context.Background()
	r.Start()
	r.Feed(ctx, c4('V', 1))
	r.Feed(ctx, c4('V', 2))
	r.Feed(ctx, c4('V', 3))
	if transcript := r.Finish(ctx); transcript == nil || len(proc.got) != 1 {
		t.Errorf("Finish: %+v, transcribed %d", transcript, len(proc.got))
	}
}
