// Package espeak speaks text with the espeak-ng command-line synthesizer,
// playing through the default audio device (Speak) or returning the audio as
// WAV (Synthesize). Robotic, but needs no model download; a stopgap until a
// neural voice (Piper, Kokoro) replaces it.
package espeak

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/voice/tts"
	"gitlab.com/tozd/go/errors"
)

const binary = "espeak-ng"

type Config struct {
	Voice          string // espeak-ng voice (default "en-us")
	WordsPerMinute int    // speaking rate (default 160)
	Logger         *slog.Logger
}

// Espeak is a tts.Speaker and tts.Synthesizer backed by espeak-ng.
type Espeak struct {
	path   string
	voice  string
	wpm    int
	logger *slog.Logger
}

var (
	_ tts.Speaker     = (*Espeak)(nil)
	_ tts.Synthesizer = (*Espeak)(nil)
)

// New returns espeak-ng as a speaker and synthesizer. It fails if espeak-ng
// is not on the PATH.
func New(config Config) (*Espeak, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return nil, errors.WithMessagef(errors.Prefix(err, fault.ErrServiceUnavailable), "%s not found (install it, or run with -tts none)", binary)
	}
	if config.Voice == "" {
		config.Voice = "en-us"
	}
	if config.WordsPerMinute == 0 {
		config.WordsPerMinute = 160
	}
	return &Espeak{
		path:   path,
		voice:  config.Voice,
		wpm:    config.WordsPerMinute,
		logger: config.Logger,
	}, nil
}

// Speak says text through the default audio device.
func (s *Espeak) Speak(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	// The text is a separate argv element after "--", never interpreted by
	// a shell or as an option.
	cmd := exec.CommandContext(ctx, s.path, "-v", s.voice, "-s", strconv.Itoa(s.wpm), "--", text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.WithMessagef(err, "%s (%s)", binary, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Synthesize returns text spoken as WAV file bytes (espeak-ng --stdout,
// 22.05 kHz mono 16-bit).
func (s *Espeak) Synthesize(ctx context.Context, text string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, s.path, "-v", s.voice, "-s", strconv.Itoa(s.wpm), "--stdout", "--", text)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.WithMessagef(err, "%s (%s)", binary, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
