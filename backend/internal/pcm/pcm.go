// Package pcm converts little-endian 16-bit mono PCM audio to float samples.
package pcm

import "math"

// BytesPerSample is the size of one PCM16 sample.
const BytesPerSample = 2

// ToFloat32 converts PCM16 data to float32 samples holding the raw int16
// magnitudes, without normalization (what openWakeWord's melspectrogram
// model expects).
func ToFloat32(data []byte) []float32 {
	samples := make([]float32, len(data)/BytesPerSample)
	for i := range samples {
		samples[i] = float32(int16(data[i*2]) | int16(data[i*2+1])<<8)
	}
	return samples
}

// ToFloat32Normalized converts PCM16 data to float32 samples in [-1.0, 1.0].
func ToFloat32Normalized(data []byte) []float32 {
	samples := make([]float32, len(data)/BytesPerSample)
	for i := range samples {
		samples[i] = float32(int16(data[i*2])|int16(data[i*2+1])<<8) / math.MaxInt16
	}
	return samples
}

// MsToBytes returns the PCM16 byte length of ms milliseconds of audio.
func MsToBytes(ms, sampleRate int) int {
	return ms * sampleRate / 1000 * BytesPerSample
}

// BytesToSeconds returns the duration of n bytes of PCM16 audio.
func BytesToSeconds(n, sampleRate int) float64 {
	return float64(n) / float64(sampleRate*BytesPerSample)
}
