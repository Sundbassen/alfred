package debounce

import (
	"context"
	"encoding/binary"
	"math"
	"math/rand"
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

// noise generates ms milliseconds of uniform white noise with the given
// RMS (normalized) as PCM16LE bytes.
func noise(rng *rand.Rand, rms float64, ms, sampleRate int) []byte {
	n := ms * sampleRate / 1000
	amplitude := rms * math.Sqrt(3) // uniform [-a, a] has RMS a/sqrt(3)
	data := make([]byte, n*2)
	for i := 0; i < n; i++ {
		s := int16((rng.Float64()*2 - 1) * amplitude * math.MaxInt16)
		binary.LittleEndian.PutUint16(data[i*2:], uint16(s))
	}
	return data
}

// TestAdaptiveFloorLearnsNoiseBed verifies that a steady noise bed above
// the static threshold becomes "silent" once the floor adapts, while speech
// level bursts stay loud and do not drag the floor up.
func TestAdaptiveFloorLearnsNoiseBed(t *testing.T) {
	const sampleRate = 16000
	ctx := context.Background()
	rng := rand.New(rand.NewSource(1))

	detector := NewRMSSilenceDetector(RMSConfig{
		SilenceThreshold: 0.015,
		SampleRate:       sampleRate,
		Adaptive:         true,
	}).(*rmsSilenceDetector)

	// 0.03 RMS noise is loud for the static 0.015 threshold, but after it
	// has been heard it defines the floor.
	for i := 0; i < 100; i++ { // 3 s of 30 ms chunks
		detector.IsSilent(ctx, noise(rng, 0.03, 30, sampleRate))
	}
	if !detector.IsSilent(ctx, noise(rng, 0.03, 30, sampleRate)) {
		t.Fatalf("noise bed should be silent after adaptation (floor %.4f)", detector.floor.value())
	}

	// Speech-like bursts (0.15 RMS, 1 s) separated by short noise gaps,
	// for longer than the floor window.
	for burst := 0; burst < 4; burst++ {
		for i := 0; i < 33; i++ {
			if detector.IsSilent(ctx, noise(rng, 0.15, 30, sampleRate)) {
				t.Fatalf("burst %d chunk %d classified silent (floor %.4f)", burst, i, detector.floor.value())
			}
		}
		for i := 0; i < 5; i++ {
			detector.IsSilent(ctx, noise(rng, 0.03, 30, sampleRate))
		}
	}
	if floor := detector.floor.value(); floor > 0.04 {
		t.Errorf("floor rose to %.4f during speech, want ~0.03", floor)
	}
}

// TestAdaptiveFloorNeverBelowStaticThreshold verifies the configured
// threshold acts as the minimum in a very quiet room.
func TestAdaptiveFloorNeverBelowStaticThreshold(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewSource(2))
	detector := NewRMSSilenceDetector(RMSConfig{
		SilenceThreshold: 0.015,
		SampleRate:       16000,
		Adaptive:         true,
	})
	for i := 0; i < 100; i++ {
		detector.IsSilent(ctx, noise(rng, 0.001, 30, 16000))
	}
	// 0.01 is 10x the floor but below the static minimum.
	if !detector.IsSilent(ctx, noise(rng, 0.01, 30, 16000)) {
		t.Error("audio below the static threshold must stay silent")
	}
}
