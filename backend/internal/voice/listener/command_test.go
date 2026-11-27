package listener

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"testing/synctest"

	"github.com/sundbassen/alfred/internal/voice"
)

// Chunks in these tests encode their own classification: the first byte is
// 'S' for silence or 'V' for voice, 'W' marks the chunk on which the fake
// activator fires (voice, score 0.5), and 'N' is voice scoring 0.01 (a near
// miss).

type fakeActivator struct {
	fed    [][]byte
	resets int
}

func (f *fakeActivator) Feed(_ context.Context, chunk []byte) (float32, bool, error) {
	f.fed = append(f.fed, chunk)
	switch chunk[0] {
	case 'W':
		return 0.5, true, nil
	case 'N':
		return 0.01, false, nil
	}
	return 0.001, false, nil
}

func (f *fakeActivator) Reset() { f.resets++ }

type fakeDetector struct{ calls int }

func (f *fakeDetector) IsSilent(_ context.Context, chunk []byte) bool {
	f.calls++
	return chunk[0] == 'S'
}

type fakeSink struct {
	wake, near   [][]byte
	wakeScores   []float32
	nearScores   []float32
	commands     [][]byte
	commandTexts []string
}

func (f *fakeSink) WakeWord(audio []byte, score float32) {
	f.wake = append(f.wake, audio)
	f.wakeScores = append(f.wakeScores, score)
}

func (f *fakeSink) NearMiss(audio []byte, score float32) {
	f.near = append(f.near, audio)
	f.nearScores = append(f.nearScores, score)
}

func (f *fakeSink) Command(audio []byte, transcript voice.Transcript) {
	f.commands = append(f.commands, audio)
	f.commandTexts = append(f.commandTexts, transcript.Text)
}

type fakeProcessor struct{ got [][]byte }

func (f *fakeProcessor) Transcribe(_ context.Context, data []byte) (voice.Transcript, error) {
	f.got = append(f.got, append([]byte(nil), data...))
	return voice.Transcript{Text: "ok"}, nil
}

// testCommandConfig: 4-byte chunks; 8-byte silence gap, 12-byte timeout,
// 40-byte max, no pre-roll, 8-byte minimum, no lookback, 8-byte wake word
// clips, near misses from 0.005.
func testCommandConfig() commandConfig {
	return commandConfig{
		sampleRate:        16000,
		silenceGapBytes:   8,
		timeoutBytes:      12,
		maxBytes:          40,
		preRollBytes:      0,
		minBytes:          8,
		hitClipBytes:      8,
		nearMissThreshold: 0.005,
	}
}

func c4(kind byte, v byte) []byte { return []byte{kind, v, v, v} }

type harness struct {
	act         *fakeActivator
	det         *fakeDetector
	proc        *fakeProcessor
	sink        *fakeSink
	transcripts []voice.Transcript
	ctrl        *commandController
}

func newHarness(config commandConfig) *harness {
	h := &harness{act: &fakeActivator{}, det: &fakeDetector{}, proc: &fakeProcessor{}, sink: &fakeSink{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.ctrl = newCommandController(h.act, h.det, h.proc, h.sink, config, logger,
		func(t voice.Transcript) { h.transcripts = append(h.transcripts, t) })
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
	if len(h.transcripts) != 1 || h.transcripts[0].Text != "ok" {
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

// TestLookbackWhenSpeechIsContinuous: when the activation fires mid-speech
// and speech continues, the audio just before (and including) the
// activation chunk is prepended, so a word begun before a late detection is
// not clipped.
func TestLookbackWhenSpeechIsContinuous(t *testing.T) {
	config := testCommandConfig()
	config.lookbackBytes = 8
	h := newHarness(config)

	h.feed(c4('S', 0), c4('V', 1), c4('W', 2))             // "hey alfred wh-"
	h.feed(c4('V', 3), c4('V', 4), c4('S', 5), c4('S', 6)) // "-at time is it"

	if len(h.proc.got) != 1 {
		t.Fatalf("Process called %d times, want 1", len(h.proc.got))
	}
	want := bytes.Join([][]byte{c4('V', 1), c4('W', 2), c4('V', 3), c4('V', 4), c4('S', 5), c4('S', 6)}, nil)
	if !bytes.Equal(h.proc.got[0], want) {
		t.Errorf("command = %q\nwant      %q", h.proc.got[0], want)
	}
}

// TestNoLookbackAfterPause: when the speaker pauses after the wake word,
// nothing from before the activation is added.
func TestNoLookbackAfterPause(t *testing.T) {
	config := testCommandConfig()
	config.lookbackBytes = 8
	h := newHarness(config)

	h.feed(c4('V', 1), c4('W', 2), c4('S', 3)) // "hey alfred" ...
	h.feed(c4('V', 4), c4('V', 5), c4('S', 6), c4('S', 7))

	if len(h.proc.got) != 1 {
		t.Fatalf("Process called %d times, want 1", len(h.proc.got))
	}
	want := bytes.Join([][]byte{c4('V', 4), c4('V', 5), c4('S', 6), c4('S', 7)}, nil)
	if !bytes.Equal(h.proc.got[0], want) {
		t.Errorf("command = %q\nwant      %q", h.proc.got[0], want)
	}
}

// TestSinkReceivesWakeWordClipAndCommand: the wake word clip is the tail of
// the listening utterance; the saved command is exactly what was
// transcribed.
func TestSinkReceivesWakeWordClipAndCommand(t *testing.T) {
	h := newHarness(testCommandConfig())

	h.feed(c4('V', 0), c4('V', 1), c4('W', 2))
	h.feed(c4('V', 3), c4('S', 4), c4('S', 5))

	if len(h.sink.wake) != 1 || h.sink.wakeScores[0] != 0.5 {
		t.Fatalf("wake word clips = %d (scores %v), want 1 with score 0.5", len(h.sink.wake), h.sink.wakeScores)
	}
	if want := bytes.Join([][]byte{c4('V', 1), c4('W', 2)}, nil); !bytes.Equal(h.sink.wake[0], want) {
		t.Errorf("wake word clip = %q, want the last 8 bytes %q", h.sink.wake[0], want)
	}
	if len(h.sink.commands) != 1 || !bytes.Equal(h.sink.commands[0], h.proc.got[0]) || h.sink.commandTexts[0] != "ok" {
		t.Errorf("saved commands = %q %v, want the transcribed audio with text ok", h.sink.commands, h.sink.commandTexts)
	}
}

// TestNearMiss: a listening utterance that scores above the near-miss
// threshold without a detection is saved; one that stays below is not.
func TestNearMiss(t *testing.T) {
	h := newHarness(testCommandConfig())

	h.feed(c4('V', 1), c4('V', 2), c4('S', 3), c4('S', 4)) // ordinary speech
	if len(h.sink.near) != 0 {
		t.Fatalf("ordinary speech saved as near miss")
	}

	h.feed(c4('V', 5), c4('N', 6), c4('S', 7), c4('S', 8)) // weak "hey alfred"
	if len(h.sink.near) != 1 || h.sink.nearScores[0] != 0.01 {
		t.Fatalf("near misses = %d (scores %v), want 1 with score 0.01", len(h.sink.near), h.sink.nearScores)
	}
	want := bytes.Join([][]byte{c4('V', 5), c4('N', 6), c4('S', 7), c4('S', 8)}, nil)
	if !bytes.Equal(h.sink.near[0], want) {
		t.Errorf("near miss = %q, want %q", h.sink.near[0], want)
	}
	if len(h.proc.got) != 0 {
		t.Error("a near miss must not start a command")
	}
}

// TestFollowUpWithoutWakeWord: after Alfred asks something, the answer is
// recorded without the wake word; with no answer it times out back to
// listening.
func TestFollowUpWithoutWakeWord(t *testing.T) {
	h := newHarness(testCommandConfig())
	h.ctrl.followUp()
	h.feed(c4('V', 1), c4('V', 2), c4('S', 3), c4('S', 4)) // "the shopping list"
	if len(h.proc.got) != 1 || h.ctrl.phase != phaseListening {
		t.Fatalf("transcribed %d, phase %v; want the answer transcribed and back to listening", len(h.proc.got), h.ctrl.phase)
	}
	if len(h.act.fed) != 0 {
		t.Errorf("activator fed %d chunks during the answer, want 0", len(h.act.fed))
	}

	h.ctrl.followUp()
	h.feed(c4('S', 5), c4('S', 6), c4('S', 7))
	if len(h.proc.got) != 1 || h.ctrl.phase != phaseListening {
		t.Errorf("no answer: transcribed %d, phase %v; want a timeout back to listening", len(h.proc.got), h.ctrl.phase)
	}

	// A follow-up request while a command is recording changes nothing.
	h.feed(c4('W', 8), c4('V', 9))
	h.ctrl.followUp()
	h.feed(c4('S', 10), c4('S', 11))
	if len(h.proc.got) != 2 {
		t.Errorf("transcribed %d, want the command in progress finished normally", len(h.proc.got))
	}
}

// TestListenReachesTheCommandLoop: Service.Listen starts a command without
// the wake word in the running command loop.
func TestListenReachesTheCommandLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(testCommandConfig())
		s := &service{
			audioChan:       make(chan []byte),
			listenChan:      make(chan struct{}, 1),
			transcriptChan:  make(chan voice.Transcript, 1),
			silenceDetector: h.det,
			transcriber:     h.proc,
			wakeWord:        h.act,
			command:         testCommandConfig(),
			sink:            h.sink,
			logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go s.commandLoop(ctx)

		s.Listen()
		synctest.Wait() // the loop took the request
		for _, chunk := range [][]byte{c4('V', 1), c4('V', 2), c4('S', 3), c4('S', 4)} {
			s.audioChan <- chunk
		}
		synctest.Wait()
		if len(h.proc.got) != 1 {
			t.Errorf("transcribed %d commands, want the answer without a wake word", len(h.proc.got))
		}
	})
}
