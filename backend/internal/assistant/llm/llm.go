// Package llm recognizes intents with a language model, for transcripts
// the phrase templates miss (paraphrases, speech recognition errors). The
// recognition logic here is model-agnostic: it describes the registered
// intents in a static prompt, constrains the answer to a JSON schema, and
// validates it. The model itself is behind the Model interface, so
// switching models is a new Model implementation plus a change in
// cmd/internal/wiring.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

// Model answers a prompt with JSON matching the request's schema.
// Implementations return fault categories: fault.ErrServiceUnavailable
// when the model can't answer right now (not loaded, not running).
type Model interface {
	Generate(ctx context.Context, req Request) ([]byte, error)
}

// Request is one prompt for the model.
type Request struct {
	// System describes the intents. It is identical for every request
	// within a run, so a model server can cache its prefix.
	System string
	// User is the transcript.
	User string
	// Schema constrains the answer.
	Schema *Schema
}

// Schema is the subset of JSON Schema the recognizer needs.
type Schema struct {
	Type                 string             `json:"type"`
	Description          string             `json:"description,omitempty"`
	Enum                 []string           `json:"enum,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	AdditionalProperties *bool              `json:"additionalProperties,omitempty"`
}

// noIntent is the answer for a transcript that matches no intent.
const noIntent = "none"

// maxExamples bounds the example phrases per intent in the prompt.
const maxExamples = 4

// Config configures a Recognizer.
type Config struct {
	// Name identifies the model in Recognition.By and logs, e.g. "ministral".
	Name string
	// Timeout bounds one recognition (default 3 s); slower answers fall
	// through to the next recognizer.
	Timeout time.Duration
	Logger  *slog.Logger
}

// Recognizer is an app.Recognizer backed by a Model.
type Recognizer struct {
	model   Model
	cfg     Config
	routes  map[string]assistant.Route
	system  string
	schema  *Schema
	timeout time.Duration
}

// New builds a recognizer for the router's intents (assistant.Router.Intents).
func New(routes []assistant.Route, model Model, cfg Config) *Recognizer {
	byName := make(map[string]assistant.Route, len(routes))
	for _, rt := range routes {
		byName[rt.Name] = rt
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	return &Recognizer{
		model:   model,
		cfg:     cfg,
		routes:  byName,
		system:  systemPrompt(routes),
		schema:  answerSchema(routes),
		timeout: timeout,
	}
}

// answer is the JSON the model produces.
type answer struct {
	Intent string            `json:"intent"`
	Slots  map[string]string `json:"slots"`
}

// Recognize asks the model which intent the transcript means. It returns
// fault.ErrNotFound when the model picks none or its answer fails
// validation, fault.ErrTimeout when it is too slow, and the model's error
// (e.g. fault.ErrServiceUnavailable) otherwise; all of these fall through
// to the next recognizer.
func (r *Recognizer) Recognize(ctx context.Context, transcript string) (assistant.Recognition, error) {
	normalized := match.Normalize(transcript)
	if normalized == "" {
		return assistant.Recognition{}, errors.WithMessage(fault.ErrNotFound, "empty transcript")
	}

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	start := time.Now()
	data, err := r.model.Generate(ctx, Request{System: r.system, User: transcript, Schema: r.schema})
	took := time.Since(start)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return assistant.Recognition{}, errors.WithMessagef(fault.ErrTimeout, "%s took over %s", r.cfg.Name, r.timeout)
	}
	if err != nil {
		return assistant.Recognition{}, errors.WithMessage(err, r.cfg.Name)
	}

	rec, err := r.validate(data, normalized)
	if err != nil {
		r.log().Info("Intent model answer rejected", slog.String("model", r.cfg.Name),
			slog.String("transcript", transcript), slog.String("answer", string(data)),
			slog.Duration("took", took), slog.Any("reason", err))
		return assistant.Recognition{}, err
	}
	r.log().Info("Intent model answered", slog.String("model", r.cfg.Name),
		slog.String("intent", rec.Intent), slog.Any("slots", rec.Slots), slog.Duration("took", took))
	return rec, nil
}

// validate turns the model's answer into a Recognition, rejecting anything
// that could make Alfred do the wrong thing: an unknown intent, slots that
// don't fit the intent, or slot values the user never said.
func (r *Recognizer) validate(data []byte, normalized string) (assistant.Recognition, error) {
	var ans answer
	if err := json.Unmarshal(data, &ans); err != nil {
		return assistant.Recognition{}, errors.Prefix(errors.WithMessage(err, "model answer is not valid JSON"),
			fault.ErrNotFound, fault.ErrDecoding)
	}
	if ans.Intent == noIntent || ans.Intent == "" {
		return assistant.Recognition{}, errors.WithMessage(fault.ErrNotFound, "model matched no intent")
	}
	rt, ok := r.routes[ans.Intent]
	if !ok {
		return assistant.Recognition{}, errors.WithMessagef(fault.ErrNotFound, "model chose unknown intent %q", ans.Intent)
	}

	// The schema allows every intent's slots; keep the non-empty ones and
	// require exactly this intent's.
	slots := make(map[string]string)
	for name, value := range ans.Slots {
		if value = match.Normalize(value); value != "" {
			slots[name] = value
		}
	}
	for name, value := range slots {
		if !slices.Contains(rt.Slots, name) {
			return assistant.Recognition{}, errors.WithMessagef(fault.ErrNotFound, "intent %q has no slot %q", rt.Name, name)
		}
		if !containsWords(normalized, value) {
			return assistant.Recognition{}, errors.WithMessagef(fault.ErrNotFound,
				"slot %s=%q is not in the transcript", name, value)
		}
	}
	for _, name := range rt.Slots {
		if _, ok := slots[name]; !ok {
			return assistant.Recognition{}, errors.WithMessagef(fault.ErrNotFound, "intent %q is missing slot %q", rt.Name, name)
		}
	}
	if len(slots) == 0 {
		slots = nil
	}
	return assistant.Recognition{Intent: rt.Name, Slots: slots, Score: 1, By: r.cfg.Name}, nil
}

// containsWords reports whether value's words appear consecutively in text
// (both normalized).
func containsWords(text, value string) bool {
	return strings.Contains(" "+text+" ", " "+value+" ")
}

func (r *Recognizer) log() *slog.Logger {
	if r.cfg.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.cfg.Logger
}

// systemPrompt describes the intents. It must not vary between requests.
func systemPrompt(routes []assistant.Route) string {
	var b strings.Builder
	b.WriteString("You are the intent recognizer of Alfred, a voice assistant. ")
	b.WriteString("The user message is a spoken command transcribed by speech recognition; it may contain recognition errors. ")
	b.WriteString("Pick the one intent below that the command clearly asks for and fill in its slots with words from the command. ")
	b.WriteString(`If the command does not clearly ask for one of these intents, answer intent "none". `)
	b.WriteString("Never invent slot values: copy them from the command. Answer with JSON only.\n\nIntents:\n")
	for _, rt := range routes {
		fmt.Fprintf(&b, "- %s: %s.", rt.Name, strings.TrimSuffix(rt.Spec.Description, "."))
		if len(rt.Slots) > 0 {
			fmt.Fprintf(&b, " Slots: %s.", strings.Join(rt.Slots, ", "))
		}
		if examples := rt.Spec.Phrases; len(examples) > 0 {
			if len(examples) > maxExamples {
				examples = examples[:maxExamples]
			}
			fmt.Fprintf(&b, " Examples: %q.", examples)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// answerSchema allows any registered intent (or "none") and the union of
// all slots; validate checks which slots fit the chosen intent.
func answerSchema(routes []assistant.Route) *Schema {
	names := make([]string, 0, len(routes)+1)
	slots := make(map[string]*Schema)
	for _, rt := range routes {
		names = append(names, rt.Name)
		for _, s := range rt.Slots {
			slots[s] = &Schema{Type: "string"}
		}
	}
	names = append(names, noIntent)
	no := false
	return &Schema{
		Type: "object",
		Properties: map[string]*Schema{
			"intent": {Type: "string", Enum: names},
			"slots":  {Type: "object", Properties: slots, AdditionalProperties: &no},
		},
		Required:             []string{"intent", "slots"},
		AdditionalProperties: &no,
	}
}
