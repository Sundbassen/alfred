// Package recordingactivator decorates a speech.Activator to save every
// utterance buffer it scores as a wav file. It exists to collect real-voice
// wake word samples for training (--custom-samples): run the app in record
// mode, say the wake word repeatedly, and every silence-delimited utterance
// is written to disk regardless of score. No score filtering is applied on
// purpose - low-scoring utterances are the most valuable training samples,
// since they are the ones the current model misses.
package recordingactivator

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/sundbassen/alfred/component/speech"
)

type activator struct {
	inner      speech.Activator
	dir        string
	sampleRate int
	logger     *slog.Logger
}

type Config struct {
	Inner      speech.Activator // The activator to delegate scoring to
	Dir        string           // Directory to write wav files into (created if missing)
	SampleRate int              // Sample rate of the PCM data (must match microphone)
	Logger     *slog.Logger     // Logger instance
}

func New(config Config) speech.Activator {
	if err := os.MkdirAll(config.Dir, 0o755); err != nil {
		config.Logger.Error("Failed to create recordings directory",
			slog.String("dir", config.Dir), slog.Any("error", err))
		return nil
	}

	config.Logger.Warn("Recording mode active: every detected utterance is saved to disk",
		slog.String("dir", config.Dir))

	return &activator{
		inner:      config.Inner,
		dir:        config.Dir,
		sampleRate: config.SampleRate,
		logger:     config.Logger,
	}
}

func (a *activator) Activate(ctx context.Context, data []byte) (bool, error) {
	activated, err := a.inner.Activate(ctx, data)

	name := fmt.Sprintf("%s_error.wav", time.Now().Format("20060102-150405.000"))
	if err == nil {
		// The inner activator does not expose its score; encode the
		// activation outcome instead so samples can be split into
		// detected/missed when reviewing.
		outcome := "miss"
		if activated {
			outcome = "hit"
		}
		name = fmt.Sprintf("%s_%s.wav", time.Now().Format("20060102-150405.000"), outcome)
	}

	path := filepath.Join(a.dir, name)
	if saveErr := a.saveWav(path, data); saveErr != nil {
		a.logger.Error("Failed to save recording", slog.String("path", path), slog.Any("error", saveErr))
	} else {
		a.logger.Info("Utterance recorded",
			slog.String("path", path),
			slog.Float64("seconds", float64(len(data))/(float64(a.sampleRate)*2)))
	}

	return activated, err
}

// saveWav writes little-endian 16-bit mono PCM data as a wav file.
func (a *activator) saveWav(path string, data []byte) error {
	const (
		bitsPerSample = 16
		numChannels   = 1
	)
	byteRate := a.sampleRate * numChannels * bitsPerSample / 8

	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+len(data)))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(header[20:22], 1)  // PCM format
	binary.LittleEndian.PutUint16(header[22:24], numChannels)
	binary.LittleEndian.PutUint32(header[24:28], uint32(a.sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(header[32:34], numChannels*bitsPerSample/8)
	binary.LittleEndian.PutUint16(header[34:36], bitsPerSample)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(len(data)))

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := file.Write(header); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}
