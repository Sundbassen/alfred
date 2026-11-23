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

// Write saves little-endian 16-bit mono PCM data as a wav file.
func Write(path string, data []byte, sampleRate int) error {
	byteRate := sampleRate * numChannels * bitsPerSample / 8

	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+len(data)))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(header[20:22], 1)  // PCM format
	binary.LittleEndian.PutUint16(header[22:24], numChannels)
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(header[32:34], numChannels*bitsPerSample/8)
	binary.LittleEndian.PutUint16(header[34:36], bitsPerSample)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(len(data)))

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := file.Write(header); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// Read returns the PCM data of a 16-bit mono wav file at the given sample
// rate, walking the RIFF chunks so files with extra chunks (LIST, fact) are
// handled.
func Read(path string, sampleRate int) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, errors.WithMessagef(fault.ErrDecoding, "%s: not a RIFF/WAVE file", path)
	}

	var formatOK bool
	for off := 12; off+8 <= len(raw); {
		id := string(raw[off : off+4])
		size := int(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		body := raw[off+8 : min(off+8+size, len(raw))]

		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, errors.WithMessagef(fault.ErrDecoding, "%s: short fmt chunk", path)
			}
			channels := binary.LittleEndian.Uint16(body[2:4])
			rate := binary.LittleEndian.Uint32(body[4:8])
			bits := binary.LittleEndian.Uint16(body[14:16])
			if channels != numChannels || int(rate) != sampleRate || bits != bitsPerSample {
				return nil, errors.WithMessagef(fault.ErrInvalidValue, "%s: need %d Hz mono s16, got %d Hz, %d channels, %d bits",
					path, sampleRate, rate, channels, bits)
			}
			formatOK = true
		case "data":
			if !formatOK {
				return nil, errors.WithMessagef(fault.ErrDecoding, "%s: data chunk before fmt chunk", path)
			}
			return body, nil
		}
		off += 8 + size + size%2
	}
	return nil, errors.WithMessagef(fault.ErrDecoding, "%s: no data chunk", path)
}
