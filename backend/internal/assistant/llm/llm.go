// Package llm recognizes intents with a language model, for transcripts
// the phrase templates miss (paraphrases, speech recognition errors). The
// recognition logic here is model-agnostic: it describes the registered
// intents in a static prompt, constrains the answer to a JSON schema, and
// validates it. The model itself is behind the Model interface, so
// switching models is a new Model implementation plus a change in
// cmd/internal/wiring.
package llm

import (
	"bytes"
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
	Generate(ctx context.Context, req Request) (Result, error)
}

// Request is one prompt for the model.
type Request struct {
	// System is the instructions, e.g. the intents. It is identical for
	// every request of one kind within a run, so a model server can cache
	// its prefix.
	System string
	// User is the transcript.
	User string
	// Schema constrains the answer.
	Schema *Schema
	// MaxTokens bounds the answer (0: the model's default for short JSON).
	MaxTokens int
	// Confidence asks for each generated token's log-probability
	// (Result.Tokens), see TextConfidence.
	Confidence bool
}

// Result is the model's answer.
type Result struct {
	JSON []byte
	// Tokens are the generated tokens with their log-probabilities, if the
	// request asked for Confidence. Their text may include formatting the
	// model server strips from JSON (e.g. a ```json fence).
	Tokens []Token
}

// Token is one generated token.
type Token struct {
	Text    string
	Logprob float64
}

// Schema is the subset of JSON Schema the model clients need.
type Schema struct {
	// AnyOf makes the answer one of several shapes (e.g. one per kind), so
	// fields of one kind can't appear in another.
	AnyOf                []*Schema          `json:"anyOf,omitempty"`
	Type                 string             `json:"type,omitempty"`
	Description          string             `json:"description,omitempty"`
	Enum                 []string           `json:"enum,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	AdditionalProperties *bool              `json:"additionalProperties,omitempty"`
	// Order is the order of Properties in the JSON (default: sorted). A
	// model writes the fields in this order, so put the deciding one (e.g.
	// a "kind") first: written after the others, it would come too late to
	// steer them.
	Order []string `json:"-"`
}

// MarshalJSON writes the schema with its properties in Order.
func (s *Schema) MarshalJSON() ([]byte, error) {
	type plain Schema // without this method
	if len(s.Properties) == 0 {
		return json.Marshal((*plain)(s))
	}
	rest := *s
	rest.Properties = nil
	head, err := json.Marshal((*plain)(&rest))
	if err != nil {
		return nil, err
	}
	keys := slices.Clone(s.Order)
	var others []string
	for k := range s.Properties {
		if !slices.Contains(keys, k) {
			others = append(others, k)
		}
	}
	slices.Sort(others)
	keys = append(keys, others...)

	var b bytes.Buffer
	b.WriteString(`{"properties":{`)
	for i, k := range keys {
		prop, ok := s.Properties[k]
		if !ok {
			continue
		}
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(k)
		value, err := json.Marshal(prop)
		if err != nil {
			return nil, err
		}
		b.Write(name)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	if len(head) > 2 { // more than "{}"
		b.WriteByte(',')
		b.Write(head[1:])
	} else {
		b.WriteByte('}')
	}
	return b.Bytes(), nil
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
	res, err := r.model.Generate(ctx, Request{System: r.system, User: transcript, Schema: r.schema})
	data := res.JSON
	took := time.Since(start)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		r.log().Warn("Intent model timed out", slog.String("model", r.cfg.Name),
			slog.String("transcript", transcript), slog.Duration("timeout", r.timeout))
		return assistant.Recognition{}, errors.WithMessagef(fault.ErrTimeout, "%s took over %s", r.cfg.Name, r.timeout)
	}
	if err != nil {
		r.log().Warn("Intent model failed", slog.String("model", r.cfg.Name),
			slog.String("transcript", transcript), slog.Duration("took", took), slog.String("error", err.Error()))
		return assistant.Recognition{}, errors.WithMessage(err, r.cfg.Name)
	}

	rec, err := r.validate(data, normalized)
	if err != nil {
		r.log().Info("Intent model answer rejected", slog.String("model", r.cfg.Name),
			slog.String("transcript", transcript), slog.String("answer", string(data)),
			slog.Duration("took", took), slog.String("reason", err.Error()))
		return assistant.Recognition{}, err
	}
	r.log().Info("Intent model answered", slog.String("model", r.cfg.Name),
		slog.String("intent", rec.Intent), slog.Any("slots", rec.Slots), slog.Duration("took", took))
	return rec, nil
}

// Warm sends one throwaway request so the model server processes and
// caches the (static) system prompt before the first real command; that
// first pass is the slow part (several seconds on a CPU).
func (r *Recognizer) Warm(ctx context.Context) error {
	start := time.Now()
	if _, err := r.model.Generate(ctx, Request{System: r.system, User: "hello", Schema: r.schema}); err != nil {
		return errors.WithMessage(err, "warm up "+r.cfg.Name)
	}
	r.log().Info("Intent model warmed up", slog.String("model", r.cfg.Name), slog.Duration("took", time.Since(start)))
	return nil
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
	// require this intent's required ones (optional ones may be missing).
	slots := make(map[string]string)
	for name, value := range ans.Slots {
		if value = match.Normalize(value); value != "" {
			slots[name] = value
		}
	}
	for name, value := range slots {
		if !slices.ContainsFunc(rt.Slots, func(s assistant.Slot) bool { return s.Name == name }) {
			return assistant.Recognition{}, errors.WithMessagef(fault.ErrNotFound, "intent %q has no slot %q", rt.Name, name)
		}
		if !containsWords(normalized, value) {
			return assistant.Recognition{}, errors.WithMessagef(fault.ErrNotFound,
				"slot %s=%q is not in the transcript", name, value)
		}
	}
	for _, s := range rt.Slots {
		if _, ok := slots[s.Name]; !ok && !s.Optional {
			return assistant.Recognition{}, errors.WithMessagef(fault.ErrNotFound, "intent %q is missing slot %q", rt.Name, s.Name)
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
	// Asking for compact JSON saves output tokens but made Ministral 3 3B
	// miss intents ("turn the music off" -> none); keep the plain request.
	b.WriteString("Never invent slot values: copy them from the command. Answer with JSON only.\n\nIntents:\n")
	for _, rt := range routes {
		fmt.Fprintf(&b, "- %s: %s.", rt.Name, strings.TrimSuffix(rt.Spec.Description, "."))
		if len(rt.Slots) > 0 {
			names := make([]string, len(rt.Slots))
			for i, s := range rt.Slots {
				names[i] = s.Name
				if s.Optional {
					names[i] += " (optional)"
				}
			}
			fmt.Fprintf(&b, " Slots: %s.", strings.Join(names, ", "))
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
			slots[s.Name] = &Schema{Type: "string"}
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
