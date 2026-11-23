package wiring

import (
	"context"
	"log/slog"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/llm"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/llm/llamaserver"
	"github.com/sundbassen/alfred/internal/llm/ministral"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

// Assistant is the recognizer chain over the registered skills, plus what
// the binaries need around it.
type Assistant struct {
	*app.Assistant
	// Prompt biases speech-to-text toward the templates' phrases.
	Prompt string

	server *llamaserver.Server // nil without the model tier
	warmed chan struct{}       // closed when the model is loaded and warm (or gave up)
	stop   context.CancelFunc
}

// NewAssistant builds the recognizers, tried in order: the phrase
// templates, then the local intent model (Ministral 3 3B on llama-server)
// for what they miss. Switching the model means another llm.Model
// implementation here. Without a model file the model tier is left out
// with a log line. Close stops the model server.
func NewAssistant(r *assistant.Router, cfg Config, logger *slog.Logger) (*Assistant, error) {
	templates := match.New(r.Intents())
	recognizers := []app.Recognizer{templates}
	a := &Assistant{Prompt: templates.Prompt(), warmed: make(chan struct{})}

	server, err := llamaserver.Start(llamaserver.Config{
		Binary:    cfg.IntentModel.Server,
		ModelFile: cfg.IntentModel.ModelFile,
		Threads:   cfg.IntentModel.Threads,
		Logger:    logger,
	})
	switch {
	case errors.Is(err, fault.ErrNotFound):
		logger.Info("Intent model disabled (run ./download-model.sh --llm and make install)", slog.Any("error", err))
	case err != nil:
		return nil, errors.WithMessage(err, "start intent model")
	default:
		model := llm.New(r.Intents(), ministral.New(server), llm.Config{
			Name:    ministral.Name,
			Timeout: cfg.IntentModel.Timeout,
			Logger:  logger,
		})
		recognizers = append(recognizers, model)
		a.server = server
		// Load and warm the model in the background; until then the model
		// tier falls through to "didn't catch that".
		var ctx context.Context
		ctx, a.stop = context.WithCancel(context.Background())
		go func() {
			defer close(a.warmed)
			if err := server.WaitReady(ctx); err != nil {
				return
			}
			if err := model.Warm(ctx); err != nil {
				logger.Warn("Intent model warm-up failed", slog.String("error", err.Error()))
			}
		}()
	}
	if a.server == nil {
		close(a.warmed)
	}

	a.Assistant = app.NewAssistant(r, recognizers...)
	return a, nil
}

// WaitReady blocks until the intent model is loaded and warmed up
// (immediately without one). The live app doesn't wait: until then the
// model tier falls through.
func (a *Assistant) WaitReady(ctx context.Context) error {
	select {
	case <-a.warmed:
		return nil
	case <-ctx.Done():
		return errors.WithStack(ctx.Err())
	}
}

// Close stops the intent model server.
func (a *Assistant) Close() {
	if a.stop != nil {
		a.stop()
	}
	if a.server != nil {
		_ = a.server.Close()
	}
}
