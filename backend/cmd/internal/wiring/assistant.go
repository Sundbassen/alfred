package wiring

import (
	"context"
	"log/slog"
	"sync"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/llm"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/llm/llamaserver"
	"github.com/sundbassen/alfred/internal/llm/ministral"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/skills/answer"
)

// Model is the local language model, Ministral 3 3B on llama-server,
// shared by the intent tier and the answer skill. A nil *Model means none
// is installed. Switching the model means another llm.Model
// implementation here.
type Model struct {
	*ministral.Ministral
	server *llamaserver.Server
	logger *slog.Logger

	mu      sync.Mutex
	warmers []func(context.Context) error
	ctx     context.Context // set by warm; nil before
	stop    context.CancelFunc
	once    sync.Once
	warmed  chan struct{} // closed when loaded and warm (or given up)
}

// StartModel starts llama-server with the model in the background (loading
// takes seconds; until then the model's users fall through). Without a
// model file or llama-server it returns nil with a log line. Close stops
// it.
func StartModel(cfg Config, logger *slog.Logger) (*Model, error) {
	server, err := llamaserver.Start(llamaserver.Config{
		Binary:    cfg.IntentModel.Server,
		ModelFile: cfg.IntentModel.ModelFile,
		Threads:   cfg.IntentModel.Threads,
		// Two slots, each with its own prompt cache: the intent prompt and
		// the answer prompt don't evict each other.
		Parallel:    2,
		ContextSize: 8192,
		Logger:      logger,
	})
	if errors.Is(err, fault.ErrNotFound) {
		logger.Info("Local model disabled (run ./download-model.sh --llm and make install)", slog.String("error", err.Error()))
		return nil, nil
	}
	if err != nil {
		return nil, errors.WithMessage(err, "start the local model")
	}
	return &Model{Ministral: ministral.New(server), server: server, logger: logger, warmed: make(chan struct{})}, nil
}

// OnReady registers a warm-up (e.g. sending a prompt once so the server
// caches it) to run when the model has loaded, in registration order; see
// warm. One registered after warming started runs as soon as the model is
// ready.
func (m *Model) OnReady(warm func(context.Context) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx == nil {
		m.warmers = append(m.warmers, warm)
		return
	}
	ctx := m.ctx
	go func() {
		if m.server.WaitReady(ctx) == nil {
			m.runWarmer(ctx, warm)
		}
	}()
}

// warm runs the registered warm-ups in the background once the model has
// loaded, first (if given) the intent tier's, which the recognizer needs
// soonest. NewAssistant calls it after the skills have registered theirs;
// later calls do nothing.
func (m *Model) warm(first func(context.Context) error) {
	m.once.Do(func() {
		m.mu.Lock()
		ctx, stop := context.WithCancel(context.Background())
		m.ctx, m.stop = ctx, stop
		warmers := m.warmers
		if first != nil {
			warmers = append([]func(context.Context) error{first}, warmers...)
		}
		m.mu.Unlock()
		go func() {
			defer close(m.warmed)
			if err := m.server.WaitReady(ctx); err != nil {
				return
			}
			failed := 0
			for _, w := range warmers {
				if !m.runWarmer(ctx, w) {
					failed++
				}
			}
			if failed > 0 {
				m.logger.Warn("Local model ready, but warm-ups failed (the first answers will be slower)", slog.Int("failed", failed))
				return
			}
			m.logger.Info("Local model ready")
		}()
	})
}

// runWarmer runs one warm-up, logging a failure; it reports success.
func (m *Model) runWarmer(ctx context.Context, w func(context.Context) error) bool {
	if err := w(ctx); err != nil && ctx.Err() == nil {
		m.logger.Warn("Local model warm-up failed", slog.String("error", err.Error()))
		return false
	}
	return true
}

// Close stops the model server. A nil Model is fine.
func (m *Model) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	stop := m.stop
	m.mu.Unlock()
	if stop != nil {
		stop()
	}
	_ = m.server.Close()
}

// Assistant is the recognizer chain over the registered skills, plus what
// the binaries need around it.
type Assistant struct {
	*app.Assistant
	// Prompt biases speech-to-text toward the templates' phrases.
	Prompt string

	model *Model // nil without the model tier
}

// NewAssistant builds the recognizers, tried in order: the phrase
// templates, then the local intent model for what they miss (if model is
// not nil). It starts warming the model up, including the warm-ups the
// skills registered (see Model.OnReady), so call it after RegisterSkills.
func NewAssistant(r *assistant.Router, cfg Config, model *Model, logger *slog.Logger) (*Assistant, error) {
	templates := match.New(r.Intents())
	recognizers := []app.Recognizer{templates}
	a := &Assistant{Prompt: templates.Prompt(), model: model}
	if model != nil {
		rec := llm.New(r.Intents(), model, llm.Config{
			Name:    ministral.Name,
			Timeout: cfg.IntentModel.Timeout,
			Logger:  logger,
		})
		recognizers = append(recognizers, rec)
		model.warm(rec.Warm)
	}
	a.Assistant = app.NewAssistant(r, recognizers...)
	// What nothing recognizes goes to the answer skill (if registered), the
	// local model's answers to questions.
	if _, err := r.New((answer.Ask{}).IntentName()); err == nil {
		a.SetFallback(func(transcript string) assistant.Intent { return answer.Ask{Question: transcript} })
	}
	return a, nil
}

// WaitReady blocks until the local model is loaded and warmed up
// (immediately without one). The live app doesn't wait: until then the
// model tier falls through.
func (a *Assistant) WaitReady(ctx context.Context) error {
	if a.model == nil {
		return nil
	}
	select {
	case <-a.model.warmed:
		return nil
	case <-ctx.Done():
		return errors.WithStack(ctx.Err())
	}
}
