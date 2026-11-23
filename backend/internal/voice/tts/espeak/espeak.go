// Package espeak speaks text with the espeak-ng command-line synthesizer,
// playing through the default audio device. Robotic, but needs no model
// download; a stopgap until a neural voice (Piper, Kokoro) replaces it.
package espeak

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"

	"github.com/sundbassen/alfred/internal/voice/tts"
)

const binary = "espeak-ng"

type Config struct {
	Voice          string // espeak-ng voice (default "en-us")
	WordsPerMinute int    // speaking rate (default 160)
	Logger         *slog.Logger
}

type speaker struct {
	path   string
	voice  string
	wpm    int
	logger *slog.Logger
}

// New returns a Speaker backed by espeak-ng. It fails if espeak-ng is not
// on the PATH.
func New(config Config) (tts.Speaker, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("%s not found (install it, or run with -tts none): %w", binary, err)
	}
	if config.Voice == "" {
		config.Voice = "en-us"
	}
	if config.WordsPerMinute == 0 {
		config.WordsPerMinute = 160
	}
	return &speaker{
		path:   path,
		voice:  config.Voice,
		wpm:    config.WordsPerMinute,
		logger: config.Logger,
	}, nil
}

func (s *speaker) Speak(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	// The text is a separate argv element after "--", never interpreted by
	// a shell or as an option.
	cmd := exec.CommandContext(ctx, s.path, "-v", s.voice, "-s", strconv.Itoa(s.wpm), "--", text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", binary, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
