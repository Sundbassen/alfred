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
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/sundbassen/alfred/component/speech"
	"github.com/sundbassen/alfred/internal/wav"
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
	if saveErr := wav.Write(path, data, a.sampleRate); saveErr != nil {
		a.logger.Error("Failed to save recording", slog.String("path", path), slog.Any("error", saveErr))
	} else {
		a.logger.Info("Utterance recorded",
			slog.String("path", path),
			slog.Float64("seconds", float64(len(data))/(float64(a.sampleRate)*2)))
	}

	return activated, err
}
