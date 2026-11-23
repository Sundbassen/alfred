package speechtotext

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/sundbassen/alfred/component/speech"
)

// Chunks in these tests encode their own classification: the first byte is
// 'S' for silence or 'V' for voice, and 'W' marks the chunk on which the
// fake activator fires (voice).

type fakeActivator struct {
	fed    [][]byte
	resets int
}

func (f *fakeActivator) Feed(_ context.Context, chunk []byte) (bool, error) {
	f.fed = append(f.fed, chunk)
	return chunk[0] == 'W', nil
}

func (f *fakeActivator) Reset() { f.resets++ }

type fakeDetector struct{ calls int }

func (f *fakeDetector) IsSilent(_ context.Context, chunk []byte) bool {
	f.calls++
	return chunk[0] == 'S'
}

type fakeProcessor struct{ got [][]byte }

func (f *fakeProcessor) Process(_ context.Context, data []byte) (speech.SpeechResult, error) {
	f.got = append(f.got, append([]byte(nil), data...))
	return speech.SpeechResult{Text: "ok"}, nil
}

// testCommandConfig: 4-byte chunks; 8-byte silence gap, 12-byte timeout,
// 40-byte max, no pre-roll, 8-byte minimum.
func testCommandConfig() commandConfig {
	return commandConfig{
		sampleRate:      16000,
		silenceGapBytes: 8,
		timeoutBytes:    12,
		maxBytes:        40,
		preRollBytes:    0,
		minBytes:        8,
	}
}

func c4(kind byte, v byte) []byte { return []byte{kind, v, v, v} }

type harness struct {
	act         *fakeActivator
	det         *fakeDetector
	proc        *fakeProcessor
	transcripts []Transcript
	ctrl        *commandController
}

func newHarness(config commandConfig) *harness {
	h := &harness{act: &fakeActivator{}, det: &fakeDetector{}, proc: &fakeProcessor{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.ctrl = newCommandController(h.act, h.det, h.proc, config, logger,
		func(t Transcript) { h.transcripts = append(h.transcripts, t) })
	return h
}

func (h *harness) feed(chunks ...[]byte) {
	for _, chunk := range chunks {
		h.ctrl.handle(context.Background(), chunk)
	}
}

// TestOneBreathCommand: "hey alfred turn on the lights" with no pause; the
// activator fires mid-speech and the command is everything after it up to
// and including the silence gap.
func TestOneBreathCommand(t *testing.T) {
	h := newHarness(testCommandConfig())

	h.feed(c4('S', 0), c4('V', 1), c4('W', 2)) // "hey alfred"
	h.feed(c4('V', 3), c4('V', 4), c4('V', 5)) // "turn on the lights"
	h.feed(c4('S', 6), c4('S', 7))             // silence gap

	if len(h.proc.got) != 1 {
		t.Fatalf("Process called %d times, want 1", len(h.proc.got))
	}
	want := bytes.Join([][]byte{c4('V', 3), c4('V', 4), c4('V', 5), c4('S', 6), c4('S', 7)}, nil)
	if !bytes.Equal(h.proc.got[0], want) {
		t.Errorf("command = %q\nwant      %q", h.proc.got[0], want)
	}
	if len(h.transcripts) != 1 || h.transcripts[0].Text != "ok" || !h.transcripts[0].IsFinal {
		t.Errorf("transcripts = %+v", h.transcripts)
	}
	if h.act.resets != 1 {
		t.Errorf("activator resets = %d, want 1", h.act.resets)
	}
	if len(h.act.fed) != 3 {
		t.Errorf("activator fed %d chunks, want only the 3 before activation", len(h.act.fed))
	}
	if h.det.calls != 8 {
		t.Errorf("silence detector saw %d chunks, want all 8", h.det.calls)
	}

	// Back to listening: the next chunk goes to the activator again.
	h.feed(c4('V', 8))
	if len(h.act.fed) != 4 {
		t.Error("activator not fed after the command finished")
	}
}

// TestPauseAfterWakeWord: "hey alfred ... turn on the lights" — silence
// before the command does not end it or count toward it.
func TestPauseAfterWakeWord(t *testing.T) {
	h := newHarness(testCommandConfig())

	h.feed(c4('W', 1))
	h.feed(c4('S', 2), c4('S', 3)) // 8 bytes waiting, below the 12-byte timeout
	h.feed(c4('V', 4), c4('V', 5), c4('S', 6), c4('S', 7))

	if len(h.proc.got) != 1 {
		t.Fatalf("Process called %d times, want 1", len(h.proc.got))
	}
	want := bytes.Join([][]byte{c4('V', 4), c4('V', 5), c4('S', 6), c4('S', 7)}, nil)
	if !bytes.Equal(h.proc.got[0], want) {
		t.Errorf("command = %q\nwant      %q", h.proc.got[0], want)
	}
}

// TestNoCommandTimesOut: the wake word alone returns to listening.
func TestNoCommandTimesOut(t *testing.T) {
	h := newHarness(testCommandConfig())

	h.feed(c4('W', 1), c4('S', 2), c4('S', 3), c4('S', 4))

	if len(h.proc.got) != 0 {
		t.Fatal("nothing should be transcribed")
	}
	if h.act.resets != 1 || h.ctrl.phase != phaseListening {
		t.Fatalf("expected timeout back to listening (resets=%d phase=%v)", h.act.resets, h.ctrl.phase)
	}
}

// TestUnevenChunksHonourByteGap: the silence gap is measured in bytes.
func TestUnevenChunksHonourByteGap(t *testing.T) {
	h := newHarness(testCommandConfig())

	h.feed(c4('W', 1), []byte("VVVVVVVVVV"))
	h.feed([]byte("SSS"), []byte("SSS"))
	if len(h.proc.got) != 0 {
		t.Fatal("6 silent bytes must not end an 8-byte gap")
	}
	h.feed([]byte("SS"))
	if len(h.proc.got) != 1 {
		t.Fatal("8 silent bytes must end the command")
	}
}

// TestDrainProcessesCommandInProgress: at shutdown, a started command is
// still transcribed.
func TestDrainProcessesCommandInProgress(t *testing.T) {
	h := newHarness(testCommandConfig())

	h.feed(c4('W', 1), c4('V', 2), c4('V', 3))
	h.ctrl.drain(context.Background())

	if len(h.proc.got) != 1 || len(h.proc.got[0]) != 8 {
		t.Fatalf("drain should transcribe the 8 pending bytes, got %v", h.proc.got)
	}
}
