package conversation

import (
	"context"
	"testing"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
)

type ping struct{}

func (ping) IntentName() string { return "ping" }

type rally struct{}

func (rally) IntentName() string { return "rally" }

func newRouter() (*assistant.Router, *History) {
	r := assistant.NewRouter()
	h := NewHistory()
	r.Use(h.Middleware)
	assistant.Handle(r, assistant.Spec{Phrases: []string{"ping"}, Help: "answer pings"},
		func(context.Context, ping) (assistant.Response, error) { return assistant.Response{Text: "Pong."}, nil })
	assistant.Handle(r, assistant.Spec{Phrases: []string{"ping twice"}, Help: "play table tennis."},
		func(context.Context, rally) (assistant.Response, error) { return assistant.Response{}, nil })
	Register(r, h)
	return r, h
}

func TestRepeat(t *testing.T) {
	r, _ := newRouter()
	ctx := context.Background()
	if resp, _ := r.Dispatch(ctx, Repeat{}); resp.Text != "I haven't said anything yet." {
		t.Errorf("nothing said: %q", resp.Text)
	}
	r.Dispatch(ctx, ping{})
	r.Dispatch(ctx, NeverMind{}) // silent replies are not remembered
	if resp, _ := r.Dispatch(ctx, Repeat{}); resp.Text != "Pong." {
		t.Errorf("repeat: %q, want Pong.", resp.Text)
	}
	if resp, _ := r.Dispatch(ctx, Repeat{}); resp.Text != "Pong." {
		t.Errorf("repeat twice: %q, want Pong.", resp.Text)
	}
}

func TestHelpListsSkills(t *testing.T) {
	r, _ := newRouter()
	resp, err := r.Dispatch(context.Background(), Help{})
	if want := "I can answer pings and play table tennis."; err != nil || resp.Text != want {
		t.Errorf("help: %q, %v; want %q", resp.Text, err, want)
	}
}

func TestNeverMindIsSilent(t *testing.T) {
	r, _ := newRouter()
	if resp, err := r.Dispatch(context.Background(), NeverMind{}); err != nil || resp.Text != "" {
		t.Errorf("never mind: %+v, %v", resp, err)
	}
}

func TestPhrases(t *testing.T) {
	r, _ := newRouter()
	m := match.New(r.Intents())
	for transcript, want := range map[string]string{
		"Never mind.":      "never_mind",
		"Cancel.":          "never_mind",
		"Say that again?":  "repeat",
		"What can you do?": "help",
	} {
		if rec, err := m.Recognize(context.Background(), transcript); err != nil || rec.Intent != want {
			t.Errorf("%q: %+v, %v; want %s", transcript, rec, err, want)
		}
	}
}
