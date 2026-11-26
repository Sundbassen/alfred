// Package volumeservice implements volume.Mixer for the speaker's audio
// output.
package volumeservice

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

// sink is PipeWire's default output: what Alfred's voice and spotifyd play
// through.
const sink = "@DEFAULT_AUDIO_SINK@"

// Wpctl controls the default PipeWire output with wpctl.
type Wpctl struct {
	run func(ctx context.Context, args ...string) ([]byte, error)
}

// WpctlConfig says where wpctl is.
type WpctlConfig struct {
	// Path is the wpctl binary; empty when it isn't installed (it comes with
	// WirePlumber).
	Path string
}

// WpctlPath returns the wpctl binary on PATH, or "" if there is none.
func WpctlPath() string {
	path, err := exec.LookPath("wpctl")
	if err != nil {
		return ""
	}
	return path
}

// NewWpctl returns a Wpctl running the configured binary.
func NewWpctl(cfg WpctlConfig) *Wpctl {
	path := cfg.Path
	return &Wpctl{run: func(ctx context.Context, args ...string) ([]byte, error) {
		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, errors.Prefix(errors.WithMessagef(err, "wpctl %s (%s)", strings.Join(args, " "), strings.TrimSpace(stderr.String())), fault.ErrServiceUnavailable)
		}
		return out, nil
	}}
}

// Volume returns the output's level (0–1) and whether it is muted.
func (w *Wpctl) Volume(ctx context.Context) (float64, bool, error) {
	out, err := w.run(ctx, "get-volume", sink)
	if err != nil {
		return 0, false, err
	}
	return parseVolume(string(out))
}

// SetVolume sets the level, capped at 100 %.
func (w *Wpctl) SetVolume(ctx context.Context, level float64) error {
	_, err := w.run(ctx, "set-volume", "--limit", "1.0", sink, strconv.FormatFloat(level, 'f', 2, 64))
	return err
}

// SetMute mutes or unmutes the output.
func (w *Wpctl) SetMute(ctx context.Context, muted bool) error {
	value := "0"
	if muted {
		value = "1"
	}
	_, err := w.run(ctx, "set-mute", sink, value)
	return err
}

// parseVolume parses wpctl get-volume output: "Volume: 0.40" or
// "Volume: 0.40 [MUTED]".
func parseVolume(out string) (float64, bool, error) {
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[0] != "Volume:" {
		return 0, false, errors.WithMessagef(fault.ErrDecoding, "wpctl get-volume: %q", strings.TrimSpace(out))
	}
	level, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, false, errors.Prefix(errors.WithMessagef(err, "wpctl get-volume: %q", strings.TrimSpace(out)), fault.ErrDecoding)
	}
	return level, strings.Contains(out, "[MUTED]"), nil
}
