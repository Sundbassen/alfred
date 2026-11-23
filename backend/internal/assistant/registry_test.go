package assistant

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

type fakeCommand struct {
	id      string
	phrases []string
	reply   string
}

func (f fakeCommand) ID() string          { return f.id }
func (f fakeCommand) Description() string { return f.id }
func (f fakeCommand) Phrases() []string   { return f.phrases }
func (f fakeCommand) Execute(ctx context.Context, req Request) (Response, error) {
	return Response{Text: f.reply}, nil
}

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

func newTestRegistry() *Registry {
	return NewRegistry(
		fakeCommand{id: "time", phrases: []string{"what time is it", "what is the time"}, reply: "It's noon."},
		fakeCommand{id: "lights_on", phrases: []string{"turn on the lights"}, reply: "Done."},
	)
}

func TestMatch(t *testing.T) {
	r := newTestRegistry()
	tests := []struct {
		transcript string
		wantID     string // "" means no match
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
		match, ok := r.Match(tt.transcript)
		if tt.wantID == "" {
			if ok {
				t.Errorf("Match(%q) = %s (%.2f), want no match", tt.transcript, match.Command.ID(), match.Score)
			}
			continue
		}
		if !ok || match.Command.ID() != tt.wantID {
			t.Errorf("Match(%q) = %+v, %v; want %s", tt.transcript, match, ok, tt.wantID)
			continue
		}
		if tt.wantExact && match.Score != 1 {
			t.Errorf("Match(%q) score = %.2f, want 1", tt.transcript, match.Score)
		}
	}
}

func TestHandle(t *testing.T) {
	r := newTestRegistry()

	resp, match, err := r.Handle(context.Background(), "What time is it?")
	if err != nil {
		t.Fatal(err)
	}
	if match.Command.ID() != "time" || resp.Text != "It's noon." {
		t.Errorf("Handle = %q via %s, want \"It's noon.\" via time", resp.Text, match.Command.ID())
	}

	_, _, err = r.Handle(context.Background(), "tell me a joke")
	if !errors.Is(err, ErrNoMatch) {
		t.Errorf("Handle(unrelated) error = %v, want ErrNoMatch", err)
	}
}

func TestPrompt(t *testing.T) {
	r := NewRegistry(
		fakeCommand{id: "time", phrases: []string{"What time is it", "what is the time"}},
		fakeCommand{id: "clock", phrases: []string{"what time is it"}}, // duplicate phrase
	)
	want := "What time is it? What is the time?"
	if got := r.Prompt(); got != want {
		t.Errorf("Prompt() = %q, want %q", got, want)
	}

	if got, want := NewRegistry(fakeCommand{id: "lights", phrases: []string{"turn on the lights"}}).Prompt(),
		"Turn on the lights."; got != want {
		t.Errorf("Prompt() = %q, want %q", got, want)
	}

	if got := NewRegistry().Prompt(); got != "" {
		t.Errorf("empty registry Prompt() = %q, want empty", got)
	}

	var many []string
	for i := range 100 {
		many = append(many, strings.Repeat("word ", 3)+strconv.Itoa(i))
	}
	words := len(strings.Fields(NewRegistry(fakeCommand{id: "many", phrases: many}).Prompt()))
	if words > maxPromptWords {
		t.Errorf("long Prompt() has %d words, want at most %d", words, maxPromptWords)
	}
}

func TestNewRegistryDuplicateIDPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewRegistry with duplicate IDs did not panic")
		}
	}()
	NewRegistry(fakeCommand{id: "time"}, fakeCommand{id: "time"})
}
