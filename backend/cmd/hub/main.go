// Command hub is Alfred's hub for satellites (#54): it connects to the
// satellites in ALFRED_SATELLITES over ESPHome's native API, answers what
// they hear with the same skills and recognizers as cmd/alfred, and serves
// the spoken replies for them to play. It has no microphone of its own.
//
//	ALFRED_SATELLITES=127.0.0.1:6053 make hub-run
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/cmd/internal/wiring"
	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/hub"
	"github.com/sundbassen/alfred/internal/platform/logging"
	"github.com/sundbassen/alfred/internal/store"
	"github.com/sundbassen/alfred/internal/voice"
	"github.com/sundbassen/alfred/internal/voice/tts/espeak"
)

func main() {
	vad := flag.String("vad", "rms", "silence detector for endpointing: rms or silero")
	verbose := flag.Bool("v", false, "verbose: debug logs and whisper.cpp output")
	flag.Parse()
	logger := logging.New(*verbose)

	if err := run(*vad, *verbose, logger); err != nil {
		logger.Error("Hub failed", slog.String("error", err.Error()))
		if errors.Is(err, errNoSatellites) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// errNoSatellites is a usage error: exit code 2.
var errNoSatellites = errors.Base("no satellites: set ALFRED_SATELLITES, e.g. 127.0.0.1:6053 (see satellite/README.md)")

func run(vad string, verbose bool, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := wiring.ConfigFromEnv()
	if len(cfg.Hub.Satellites) == 0 {
		return errors.WithStack(errNoSatellites)
	}

	synth, err := espeak.New(espeak.Config{Logger: logger})
	if err != nil {
		return err
	}
	h, err := hub.New(hub.Config{
		Satellites:  cfg.Hub.Satellites,
		MediaAddr:   cfg.Hub.MediaAddr,
		Synthesizer: synth,
		NewSilenceDetector: func() (voice.SilenceDetector, error) {
			return app.NewSilenceDetector(vad, logger)
		},
		Logger: logger,
	})
	if err != nil {
		return err
	}

	db, err := store.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	model, err := wiring.StartModel(cfg, logger)
	if err != nil {
		return err
	}
	defer model.Close()

	// Timers ring on the satellites: the hub is the clock's announcer.
	router := assistant.NewRouter()
	if err := wiring.RegisterSkills(ctx, router, cfg, wiring.Deps{Output: h, DB: db, Model: model}, logger); err != nil {
		return err
	}
	asst, err := wiring.NewAssistant(router, cfg, model, logger)
	if err != nil {
		return err
	}
	transcriber, cleanup, err := app.NewTranscriber(app.Options{Prompt: asst.Prompt, Verbose: verbose}, logger)
	if err != nil {
		return err
	}
	defer cleanup()

	return h.Run(ctx, asst.Assistant, transcriber)
}
