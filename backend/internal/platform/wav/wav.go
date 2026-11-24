// Package wav reads and writes 16-bit mono PCM wav files, the format the
// whole pipeline uses.
package wav

import (
	"encoding/binary"
	"os"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"gitlab.com/tozd/go/errors"
)

const (
	bitsPerSample = 16
	numChannels   = 1
)

// Encode returns little-endian 16-bit mono PCM data as wav file bytes.
func Encode(data []byte, sampleRate int) []byte {
	byteRate := sampleRate * numChannels * bitsPerSample / 8

	out := make([]byte, 44, 44+len(data))
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(36+len(data)))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(out[20:22], 1)  // PCM format
	binary.LittleEndian.PutUint16(out[22:24], numChannels)
	binary.LittleEndian.PutUint32(out[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(out[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(out[32:34], numChannels*bitsPerSample/8)
	binary.LittleEndian.PutUint16(out[34:36], bitsPerSample)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(len(data)))
	return append(out, data...)
}

// Write saves little-endian 16-bit mono PCM data as a wav file.
func Write(path string, data []byte, sampleRate int) error {
	return errors.WithStack(os.WriteFile(path, Encode(data, sampleRate), 0o644))
}

// Read returns the PCM data of a 16-bit mono wav file at the given sample
// rate (see Decode).
func Read(path string, sampleRate int) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	data, err := Decode(raw, sampleRate)
	if err != nil {
		return nil, errors.WithMessage(err, path)
	}
	return data, nil
}

// Decode returns the PCM data of 16-bit mono wav file bytes at the given
// sample rate, walking the RIFF chunks so files with extra chunks (LIST,
// fact) are handled.
func Decode(raw []byte, sampleRate int) ([]byte, error) {
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, errors.WithMessage(fault.ErrDecoding, "not a RIFF/WAVE file")
	}

	var formatOK bool
	for off := 12; off+8 <= len(raw); {
		id := string(raw[off : off+4])
		size := int(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		body := raw[off+8 : min(off+8+size, len(raw))]

		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, errors.WithMessage(fault.ErrDecoding, "short fmt chunk")
			}
			channels := binary.LittleEndian.Uint16(body[2:4])
			rate := binary.LittleEndian.Uint32(body[4:8])
			bits := binary.LittleEndian.Uint16(body[14:16])
			if channels != numChannels || int(rate) != sampleRate || bits != bitsPerSample {
				return nil, errors.WithMessagef(fault.ErrInvalidValue, "need %d Hz mono s16, got %d Hz, %d channels, %d bits",
					sampleRate, rate, channels, bits)
			}
			formatOK = true
		case "data":
			if !formatOK {
				return nil, errors.WithMessage(fault.ErrDecoding, "data chunk before fmt chunk")
			}
			return body, nil
		}
		off += 8 + size + size%2
	}
	return nil, errors.WithMessage(fault.ErrDecoding, "no data chunk")
}
