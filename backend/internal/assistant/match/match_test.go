package match

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/sundbassen/alfred/internal/assistant"
)

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{" What time is it?", "what time is it"},
		{"What's the time?", "what is the time"},
		{"What’s the time?", "what is the time"},
		{"Hey Alfred, uh, tell me the time please.", "tell me the time"},
		{"Turn on the living-room lights!", "turn on the living room lights"},
		{"  ", ""},
		{"...", ""},
	}
	for _, tt := range tests {
		if got := normalize(tt.in); got != tt.want {
			t.Errorf("normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func routes(intents ...assistant.Route) []assistant.Route { return intents }

func route(name string, phrases ...string) assistant.Route {
	return assistant.Route{Name: name, Spec: assistant.Spec{Phrases: phrases}}
}

func newTestMatcher() *Matcher {
	return New(routes(
		route("time", "what time is it", "what is the time"),
		route("lights_on", "turn on the lights"),
	))
}

func TestRecognize(t *testing.T) {
	m := newTestMatcher()
	tests := []struct {
		transcript string
		wantName   string // "" means no match
		wantExact  bool
	}{
		{" What time is it?", "time", true},
		{"What's the time?", "time", true},
		{"what time is it now", "time", false},
		{"turn on the lights", "lights_on", true},
		{"tell me a joke", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		res, err := m.Recognize(tt.transcript)
		if tt.wantName == "" {
			if !errors.Is(err, ErrNoMatch) {
				t.Errorf("Recognize(%q) = %+v, %v; want ErrNoMatch", tt.transcript, res, err)
			}
			continue
		}
		if err != nil || res.Name != tt.wantName {
			t.Errorf("Recognize(%q) = %+v, %v; want %s", tt.transcript, res, err, tt.wantName)
			continue
		}
		if tt.wantExact && res.Score != 1 {
			t.Errorf("Recognize(%q) score = %.2f, want 1", tt.transcript, res.Score)
		}
	}
}

func TestPrompt(t *testing.T) {
	m := New(routes(
		route("time", "What time is it", "what is the time"),
		route("clock", "what time is it"), // duplicate phrase
	))
	if got, want := m.Prompt(), "What time is it? What is the time?"; got != want {
		t.Errorf("Prompt() = %q, want %q", got, want)
	}

	if got, want := New(routes(route("lights", "turn on the lights"))).Prompt(), "Turn on the lights."; got != want {
		t.Errorf("Prompt() = %q, want %q", got, want)
	}

	if got := New(nil).Prompt(); got != "" {
		t.Errorf("empty matcher Prompt() = %q, want empty", got)
	}

	var many []string
	for i := range 100 {
		many = append(many, strings.Repeat("word ", 3)+strconv.Itoa(i))
	}
	words := len(strings.Fields(New(routes(route("many", many...))).Prompt()))
	if words > maxPromptWords {
		t.Errorf("long Prompt() has %d words, want at most %d", words, maxPromptWords)
	}
}

func TestNoMatchErrorCarriesBest(t *testing.T) {
	_, err := newTestMatcher().Recognize("tell me a joke about time")
	var noMatch *NoMatchError
	if !errors.As(err, &noMatch) || noMatch.Best.Name != "time" || noMatch.Best.Score == 0 {
		t.Errorf("Recognize = %v; want a *NoMatchError with best candidate time", err)
	}
}
