package logging

import (
	"log/slog"
	"os"
)

// New returns the app logger. verbose enables Debug, which includes the
// periodic audio level and wake word score stats used for tuning
// thresholds.
func New(verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
