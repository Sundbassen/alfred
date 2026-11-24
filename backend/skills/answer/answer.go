// Package answer is the answer skill: questions no other skill covers
// (arithmetic, unit conversions, general knowledge), answered by the local
// model until a cloud model takes over what it can't (#2). The model only
// works out what is asked; code computes numbers, so they're exact, and a
// free-text answer is only said when the model was confident of it
// (llm.TextConfidence).
package answer

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/llm"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

// Ask is a question for the model. It has no phrases: transcripts no
// recognizer places come here (app.Assistant.SetFallback, set in wiring),
// and the intent model may pick it too, copying the question.
type Ask struct {
	Question string `slot:"question"`
}

func (Ask) IntentName() string { return "answer" }

// Model is the language model answering (llm.Model).
type Model interface {
	Generate(ctx context.Context, req llm.Request) (llm.Result, error)
}

// A free-text answer is said only if its tokens' geometric mean
// probability and its least likely token reach these. Tuned on known
// questions with Ministral 3 3B (answer_model_test.go): right answers
// scored a mean of 0.83–0.98 and a least of 0.44 or more, made-up ones a
// mean of 0.53–0.66 and a least of 0.14 or less.
const (
	minMean  = 0.8
	minLeast = 0.3
)

// timeout bounds one answer; free text takes a few seconds on a CPU.
const timeout = 15 * time.Second

// Texts for what can't be answered.
const (
	UnknownText = "Sorry, I don't know that yet."
	UnsureText  = "I'm not sure about that."
)

// Register adds the answer intent to r, answered by model.
func Register(r *assistant.Router, model Model, logger *slog.Logger) *Answerer {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	a := &Answerer{model: model, logger: logger}
	assistant.Handle(r, assistant.Spec{
		Description: "Answer a question no other intent covers: arithmetic, a unit conversion, or general knowledge. " +
			"Copy the user's whole question into the question slot",
		Help: "answer questions and do sums and conversions",
	}, a.ask)
	return a
}

// Answerer answers questions with the model.
type Answerer struct {
	model  Model
	logger *slog.Logger
}

// Warm sends one throwaway question so the model server caches the answer
// prompt before the first real question.
func (a *Answerer) Warm(ctx context.Context) error {
	_, err := a.model.Generate(ctx, request("what is 1 plus 1"))
	return errors.WithMessage(err, "warm up answers")
}

// reply is the model's answer: one of the shapes in schema.
type reply struct {
	Kind       string   `json:"kind"`
	Expression string   `json:"expression"`
	Value      *float64 `json:"value"`
	From       string   `json:"from"`
	To         string   `json:"to"`
	Text       string   `json:"text"`
}

func (a *Answerer) ask(ctx context.Context, in Ask) (assistant.Response, error) {
	question := match.Normalize(in.Question)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	res, err := a.model.Generate(ctx, request(in.Question))
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return assistant.Response{}, errors.WithMessagef(fault.ErrTimeout, "answer took over %s", timeout)
	}
	if err != nil {
		return assistant.Response{}, err
	}
	var r reply
	if err := json.Unmarshal(res.JSON, &r); err != nil {
		return assistant.Response{}, errors.Prefix(errors.WithMessagef(err, "model answer %s", res.JSON), fault.ErrDecoding)
	}
	log := a.logger.With(slog.String("question", question), slog.String("kind", r.Kind), slog.Duration("took", time.Since(start)))

	switch r.Kind {
	case "math":
		v, err := evaluate(r.Expression)
		if err != nil {
			log.Info("Local answer: bad expression", slog.String("expression", r.Expression), slog.String("error", err.Error()))
			return assistant.Response{Text: "Sorry, I couldn't work that out."}, nil
		}
		log.Info("Local answer", slog.String("expression", r.Expression), slog.Float64("result", v))
		return assistant.Response{Text: "That's " + formatNumber(v) + "."}, nil

	case "convert":
		if r.Value == nil {
			return assistant.Response{Text: "Sorry, I couldn't work that out."}, nil
		}
		from, to := r.From, r.To
		// Small models sometimes swap the units; the amount in the question
		// is next to the unit it's in ("30 degrees celsius").
		if u := unitAfterValue(question, *r.Value); u != "" && u == to && u != from {
			from, to = to, from
		}
		v, err := convert(*r.Value, from, to)
		if err != nil {
			log.Info("Local answer: bad conversion", slog.String("from", from), slog.String("to", to))
			return assistant.Response{Text: "Sorry, I can't convert between those."}, nil
		}
		log.Info("Local answer", slog.Float64("value", *r.Value), slog.String("from", from), slog.String("to", to))
		return assistant.Response{Text: unitName(*r.Value, from) + " is " + unitName(v, to) + "."}, nil

	case "answer":
		mean, least, ok := llm.TextConfidence(res.Tokens, "text")
		log = log.With(slog.String("text", r.Text), slog.Float64("mean", mean), slog.Float64("least", least))
		if !ok || mean < minMean || least < minLeast || strings.TrimSpace(r.Text) == "" {
			log.Info("Local answer: not confident enough")
			return assistant.Response{Text: UnsureText}, nil
		}
		log.Info("Local answer")
		return assistant.Response{Text: speakable(r.Text)}, nil
	}
	log.Info("Local answer: unknown")
	return assistant.Response{Text: UnknownText}, nil
}

// systemPrompt is static, so the model server caches it.
var systemPrompt = `You answer questions for Alfred, a voice assistant. The user message is a spoken question, transcribed by speech recognition. Answer with JSON only, choosing kind:
- "math": arithmetic. Put a plain expression in "expression" using digits, + - * / ^ ( ) and sqrt(...). "X percent of Y" is (X/100)*Y. Never compute the result yourself.
- "convert": a unit conversion. "value" is the amount given in the question (1 if none), "from" is its unit and "to" the unit asked for, using only these symbols: ` + strings.Join(unitSymbols, " ") + `. "How many grams in a pound" is value 1, from lb, to g. Never compute the result yourself.
- "answer": a factual question you are sure about. Put a short answer, one sentence, in "text".
- "unknown": anything else, or if you are not sure.`

// schema allows exactly one shape per kind, so e.g. a conversion can't
// carry a formula the model computed itself.
var schema = func() *llm.Schema {
	no := false
	kind := func(k string) *llm.Schema { return &llm.Schema{Type: "string", Enum: []string{k}} }
	obj := func(props map[string]*llm.Schema, required ...string) *llm.Schema {
		// "kind" first: the model decides what it is answering before
		// writing the rest.
		return &llm.Schema{Type: "object", Properties: props, Required: required, AdditionalProperties: &no, Order: required}
	}
	unit := &llm.Schema{Type: "string", Enum: unitSymbols}
	return &llm.Schema{AnyOf: []*llm.Schema{
		obj(map[string]*llm.Schema{"kind": kind("math"), "expression": {Type: "string"}}, "kind", "expression"),
		obj(map[string]*llm.Schema{"kind": kind("convert"), "value": {Type: "number"}, "from": unit, "to": unit},
			"kind", "value", "from", "to"),
		obj(map[string]*llm.Schema{"kind": kind("answer"), "text": {Type: "string"}}, "kind", "text"),
		obj(map[string]*llm.Schema{"kind": kind("unknown")}, "kind"),
	}}
}()

func request(question string) llm.Request {
	return llm.Request{System: systemPrompt, User: question, Schema: schema, MaxTokens: 120, Confidence: true}
}

// speakable strips markdown the model likes to add ("*Hamlet*").
func speakable(text string) string {
	text = strings.NewReplacer("**", "", "*", "", "__", "", "`", "").Replace(strings.TrimSpace(text))
	if !strings.HasSuffix(text, ".") && !strings.HasSuffix(text, "!") && !strings.HasSuffix(text, "?") {
		text += "."
	}
	return text
}
