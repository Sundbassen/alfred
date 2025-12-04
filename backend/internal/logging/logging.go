package logging

import (
	"log/slog"
	"os"
)

func New() *slog.Logger {
	return (*slog.Logger)(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))
}
