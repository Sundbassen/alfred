package debounce

import (
	"context"
	"encoding/binary"
	"math"
	"testing"
)

// tone generates one second of a sine wave as PCM16LE bytes.
func tone(freqHz float64, amplitude float64, sampleRate int) []byte {
	data := make([]byte, sampleRate*2)
	for i := 0; i < sampleRate; i++ {
		s := int16(amplitude * math.MaxInt16 * math.Sin(2*math.Pi*freqHz*float64(i)/float64(sampleRate)))
		binary.LittleEndian.PutUint16(data[i*2:], uint16(s))
	}
	return data
}

// TestHighPassSuppressesLowFrequency verifies that with the high-pass
// enabled, low-frequency rumble (footsteps) at speech-level amplitude is
// classified as silence while speech-band audio of the same amplitude is
// not.
func TestHighPassSuppressesLowFrequency(t *testing.T) {
	detector := NewRMSSilenceDetector(RMSConfig{
		SilenceThreshold: 0.015,
		HighPassHz:       200,
		SampleRate:       16000,
	})

	ctx := context.Background()

	// 50 Hz rumble at RMS ~0.07 (0.1 amplitude): loud without the filter,
	// silent with it.
	if !detector.IsSilent(ctx, tone(50, 0.1, 16000)) {
		t.Error("50 Hz rumble should be silent with 200 Hz high-pass")
	}

	// 1 kHz (speech band) at the same amplitude must stay loud.
	if detector.IsSilent(ctx, tone(1000, 0.1, 16000)) {
		t.Error("1 kHz tone should not be silent")
	}
}

// TestWithoutHighPassRumbleIsLoud documents the previous behavior: without
// the filter, the same rumble counts as speech.
func TestWithoutHighPassRumbleIsLoud(t *testing.T) {
	detector := NewRMSSilenceDetector(RMSConfig{
		SilenceThreshold: 0.015,
	})

	if detector.IsSilent(context.Background(), tone(50, 0.1, 16000)) {
		t.Error("50 Hz rumble at 0.07 RMS should be loud without high-pass")
	}
}
