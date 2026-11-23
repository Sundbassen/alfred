package listener

import (
	"bytes"
	"testing"
)

// Test configuration with tiny, easily reasoned-about sizes:
// 4-byte chunks, 8-byte (2-chunk) pre-roll, 8-byte (2-chunk) silence gap,
// 12-byte (3-chunk) minimum, 40-byte (10-chunk) maximum.
func testConfig() assemblerConfig {
	return assemblerConfig{
		preRollBytes:      8,
		silenceGapBytes:   8,
		minUtteranceBytes: 12,
		maxUtteranceBytes: 40,
	}
}

// chunk creates a 4-byte chunk filled with the given value.
func chunk(v byte) []byte {
	return bytes.Repeat([]byte{v}, 4)
}

func TestPreRollPrependedToUtterance(t *testing.T) {
	a := newUtteranceAssembler(testConfig())

	// Idle silence: 3 chunks, only the last 2 must be kept as pre-roll
	for _, v := range []byte{1, 2, 3} {
		if utt, reason := a.Add(chunk(v), true); utt != nil || reason != flushNone {
			t.Fatal("no utterance expected during idle silence")
		}
	}

	// Speech, then a silence gap long enough to flush
	a.Add(chunk(10), false)
	a.Add(chunk(11), false)
	a.Add(chunk(20), true)
	utt, reason := a.Add(chunk(21), true)

	if reason != flushSilence {
		t.Fatalf("expected flushSilence, got %v", reason)
	}
	want := bytes.Join([][]byte{
		chunk(2), chunk(3), // pre-roll: last 2 idle chunks, chunk(1) evicted
		chunk(10), chunk(11), // speech
		chunk(20), chunk(21), // trailing silence gap
	}, nil)
	if !bytes.Equal(utt, want) {
		t.Errorf("utterance = % x\nwant        = % x", utt, want)
	}
}

func TestSilenceGapRequiresMinimumDuration(t *testing.T) {
	a := newUtteranceAssembler(testConfig())

	// One speech chunk (4 bytes) then silence: gap is reached at the 2nd
	// silent chunk, but the buffer (12 bytes total) only meets the minimum
	// at the 2nd silent chunk too - flush happens exactly then, not before.
	a.Add(chunk(10), false)
	if utt, _ := a.Add(chunk(20), true); utt != nil {
		t.Fatal("must not flush below minimum utterance size")
	}
	utt, reason := a.Add(chunk(21), true)
	if reason != flushSilence || len(utt) != 12 {
		t.Fatalf("expected flush at exactly min size, got reason=%v len=%d", reason, len(utt))
	}
}

func TestForcedFlushAtMaxSize(t *testing.T) {
	a := newUtteranceAssembler(testConfig())

	// Continuous speech, never silent: must force-flush at max (10 chunks)
	for i := 0; i < 9; i++ {
		if utt, _ := a.Add(chunk(byte(i)), false); utt != nil {
			t.Fatalf("premature flush at chunk %d", i)
		}
	}
	utt, reason := a.Add(chunk(9), false)
	if reason != flushMaxDuration {
		t.Fatalf("expected flushMaxDuration, got %v", reason)
	}
	if len(utt) != 40 {
		t.Errorf("utterance length = %d, want 40", len(utt))
	}
}

func TestStateResetBetweenUtterances(t *testing.T) {
	a := newUtteranceAssembler(testConfig())

	// First utterance
	a.Add(chunk(1), true) // pre-roll
	a.Add(chunk(10), false)
	a.Add(chunk(11), false)
	a.Add(chunk(20), true)
	if utt, _ := a.Add(chunk(21), true); utt == nil {
		t.Fatal("first utterance expected")
	}

	// Second utterance must contain nothing from the first, including no
	// stale pre-roll
	a.Add(chunk(30), false)
	a.Add(chunk(31), false)
	a.Add(chunk(40), true)
	utt, _ := a.Add(chunk(41), true)
	if utt == nil {
		t.Fatal("second utterance expected")
	}
	want := bytes.Join([][]byte{chunk(30), chunk(31), chunk(40), chunk(41)}, nil)
	if !bytes.Equal(utt, want) {
		t.Errorf("second utterance carries stale state:\ngot  % x\nwant % x", utt, want)
	}
}

func TestPendingExposesIncompleteUtterance(t *testing.T) {
	a := newUtteranceAssembler(testConfig())

	a.Add(chunk(10), false)
	a.Add(chunk(11), false)
	if got := len(a.Pending()); got != 8 {
		t.Errorf("Pending length = %d, want 8", got)
	}
}

// TestMixedChunkSizes verifies byte-based accounting: the silence gap and
// pre-roll are measured in bytes regardless of how the audio is chunked.
func TestMixedChunkSizes(t *testing.T) {
	a := newUtteranceAssembler(testConfig())

	// Idle: a 6-byte chunk then a 2-byte chunk fit the 8-byte pre-roll; a
	// further 4-byte chunk evicts the 6-byte one.
	a.Add(bytes.Repeat([]byte{1}, 6), true)
	a.Add(bytes.Repeat([]byte{2}, 2), true)
	a.Add(bytes.Repeat([]byte{3}, 4), true)

	a.Add(bytes.Repeat([]byte{10}, 10), false)

	// 3 + 3 silent bytes is below the 8-byte gap; 3 more crosses it.
	if utt, _ := a.Add(bytes.Repeat([]byte{20}, 3), true); utt != nil {
		t.Fatal("flushed before the silence gap")
	}
	if utt, _ := a.Add(bytes.Repeat([]byte{21}, 3), true); utt != nil {
		t.Fatal("flushed before the silence gap")
	}
	utt, reason := a.Add(bytes.Repeat([]byte{22}, 3), true)
	if reason != flushSilence {
		t.Fatalf("expected flushSilence, got %v", reason)
	}
	want := bytes.Join([][]byte{
		bytes.Repeat([]byte{2}, 2), bytes.Repeat([]byte{3}, 4),
		bytes.Repeat([]byte{10}, 10),
		bytes.Repeat([]byte{20}, 3), bytes.Repeat([]byte{21}, 3), bytes.Repeat([]byte{22}, 3),
	}, nil)
	if !bytes.Equal(utt, want) {
		t.Errorf("utterance = % x\nwant        = % x", utt, want)
	}
}
