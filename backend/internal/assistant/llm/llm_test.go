package llm

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

var routes = []assistant.Route{
	{Name: "time", Spec: assistant.Spec{Description: "Tell the current local time", Phrases: []string{"what time is it"}}},
	{Name: "music_play", Slots: []string{"query"},
		Spec: assistant.Spec{Description: "Search for music and play it", Phrases: []string{"play {query}", "put on {query}"}}},
	{Name: "music_mix", Slots: []string{"artists"},
		Spec: assistant.Spec{Description: "Play a shuffled mix of the named artists", Phrases: []string{"play a radio with {artists}"}}},
}

// fakeModel returns a scripted answer and records requests.
type fakeModel struct {
	answer string
	err    error
	delay  time.Duration
	reqs   []Request
}

func (f *fakeModel) Generate(ctx context.Context, req Request) ([]byte, error) {
	f.reqs = append(f.reqs, req)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []byte(f.answer), f.err
}

func newRecognizer(m Model) *Recognizer {
	return New(routes, m, Config{Name: "fake", Timeout: 200 * time.Millisecond})
}

func TestRecognize(t *testing.T) {
	m := &fakeModel{answer: `{"intent":"music_play","slots":{"query":"Queen","artists":""}}`}
	rec, err := newRecognizer(m).Recognize(context.Background(), "Could you throw on some Queen?")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Intent != "music_play" || rec.Slots["query"] != "queen" || len(rec.Slots) != 1 || rec.By != "fake" {
		t.Errorf("rec = %+v", rec)
	}

	rec, err = newRecognizer(&fakeModel{answer: `{"intent":"time","slots":{}}`}).Recognize(context.Background(), "what's the clock")
	if err != nil || rec.Intent != "time" || rec.Slots != nil {
		t.Errorf("time: rec = %+v, err = %v", rec, err)
	}
}

func TestRejections(t *testing.T) {
	tests := map[string]string{
		"none":                    `{"intent":"none","slots":{}}`,
		"unknown intent":          `{"intent":"weather","slots":{}}`,
		"slot of another intent":  `{"intent":"time","slots":{"query":"queen"}}`,
		"missing slot":            `{"intent":"music_play","slots":{}}`,
		"hallucinated slot value": `{"intent":"music_play","slots":{"query":"bohemian rhapsody"}}`,
		"partial word slot value": `{"intent":"music_play","slots":{"query":"que"}}`,
		"not json":                `play queen`,
	}
	for name, ans := range tests {
		_, err := newRecognizer(&fakeModel{answer: ans}).Recognize(context.Background(), "could you throw on some queen")
		if !errors.Is(err, fault.ErrNotFound) {
			t.Errorf("%s: error = %v, want fault.ErrNotFound (falls through)", name, err)
		}
	}
	_, err := newRecognizer(&fakeModel{answer: "nope"}).Recognize(context.Background(), "x")
	if !errors.Is(err, fault.ErrDecoding) {
		t.Errorf("not json: error = %v, want also fault.ErrDecoding", err)
	}
}

func TestModelErrorsAndTimeout(t *testing.T) {
	down := &fakeModel{err: errors.WithMessage(fault.ErrServiceUnavailable, "loading")}
	if _, err := newRecognizer(down).Recognize(context.Background(), "play queen"); !errors.Is(err, fault.ErrServiceUnavailable) {
		t.Errorf("unavailable: error = %v", err)
	}
	slow := &fakeModel{answer: `{"intent":"time","slots":{}}`, delay: time.Second}
	if _, err := newRecognizer(slow).Recognize(context.Background(), "what's the clock"); !errors.Is(err, fault.ErrTimeout) {
		t.Errorf("slow: error = %v, want fault.ErrTimeout", err)
	}
}

func TestPromptAndSchema(t *testing.T) {
	m := &fakeModel{answer: `{"intent":"none","slots":{}}`}
	r := newRecognizer(m)
	_, _ = r.Recognize(context.Background(), "first")
	_, _ = r.Recognize(context.Background(), "second")

	if len(m.reqs) != 2 || m.reqs[0].System != m.reqs[1].System {
		t.Fatal("system prompt must be identical across requests (prefix caching)")
	}
	if m.reqs[1].User != "second" {
		t.Errorf("user message = %q, want the transcript", m.reqs[1].User)
	}
	for _, want := range []string{"music_play: Search for music and play it.", "Slots: query.", `"play {query}"`, `"none"`} {
		if !strings.Contains(m.reqs[0].System, want) {
			t.Errorf("system prompt lacks %q:\n%s", want, m.reqs[0].System)
		}
	}

	schema := m.reqs[0].Schema
	intents := schema.Properties["intent"].Enum
	if !slices.Contains(intents, "none") || !slices.Contains(intents, "music_mix") {
		t.Errorf("intent enum = %v", intents)
	}
	if _, ok := schema.Properties["slots"].Properties["artists"]; !ok {
		t.Errorf("slots schema lacks artists: %+v", schema.Properties["slots"])
	}
	if data, err := json.Marshal(schema); err != nil || !strings.Contains(string(data), `"additionalProperties":false`) {
		t.Errorf("schema JSON = %s, %v", data, err)
	}
}
