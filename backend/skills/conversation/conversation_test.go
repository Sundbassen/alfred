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

type asking struct{}

func (asking) IntentName() string { return "asking" }

type miss struct{}

func (miss) IntentName() string { return "miss" }

func newRouter() (*assistant.Router, *History) {
	r := assistant.NewRouter()
	h := NewHistory()
	r.Use(h.Middleware)
	assistant.Handle(r, assistant.Spec{Phrases: []string{"ping"}, Help: "answer pings"},
		func(context.Context, ping) (assistant.Response, error) { return assistant.Response{Text: "Pong."}, nil })
	assistant.Handle(r, assistant.Spec{Phrases: []string{"ping twice"}, Help: "play table tennis."},
		func(context.Context, rally) (assistant.Response, error) { return assistant.Response{}, nil })
	assistant.Handle(r, assistant.Spec{Phrases: []string{"ask"}},
		func(context.Context, asking) (assistant.Response, error) {
			return assistant.Response{Text: "Which one?", Ask: &assistant.Question{Intent: asking{}}}, nil
		})
	assistant.Handle(r, assistant.Spec{Phrases: []string{"miss"}},
		func(context.Context, miss) (assistant.Response, error) {
			return assistant.Response{}, &assistant.SpokenError{Text: "I missed."}
		})
	Register(r, h)
	return r, h
}

func TestRepeat(t *testing.T) {
	r, _ := newRouter()
	ctx := context.Background()
	say := func(intent assistant.Intent) string {
		t.Helper()
		resp, err := r.Dispatch(ctx, intent)
		if err != nil {
			t.Fatalf("%s: %v", intent.IntentName(), err)
		}
		return resp.Text
	}
	if got := say(Repeat{}); got != "I haven't said anything yet." {
		t.Errorf("nothing said: %q", got)
	}
	say(ping{})
	say(NeverMind{}) // silent replies are not remembered
	if got := say(Repeat{}); got != "Pong." {
		t.Errorf("repeat: %q, want Pong.", got)
	}
	if got := say(Repeat{}); got != "Pong." {
		t.Errorf("repeat twice: %q, want Pong.", got)
	}
	// What was said about a failure is remembered too.
	if _, err := r.Dispatch(ctx, miss{}); assistant.SpokenText(err) != "I missed." {
		t.Fatalf("miss: %v", err)
	}
	if got := say(Repeat{}); got != "I missed." {
		t.Errorf("repeat after an error: %q, want I missed.", got)
	}
	// Repeating a question asks it again.
	say(asking{})
	if resp, err := r.Dispatch(ctx, Repeat{}); err != nil || resp.Text != "Which one?" || resp.Ask == nil {
		t.Errorf("repeat a question: %+v, %v; want it asked again", resp, err)
	}
}

func TestHelpListsSkills(t *testing.T) {
	r, _ := newRouter()
	resp, err := r.Dispatch(context.Background(), Help{})
	if want := "I can answer pings, and play table tennis."; err != nil || resp.Text != want {
		t.Errorf("help: %q, %v; want %q", resp.Text, err, want)
	}
}

func TestJoinHelp(t *testing.T) {
	for _, tc := range []struct {
		helps []string
		want  string
	}{
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a, and b"},
		{[]string{"tell you the time and date", "set timers and reminders", "play music"},
			"tell you the time and date, set timers and reminders, and play music"},
	} {
		if got := joinHelp(tc.helps); got != tc.want {
			t.Errorf("joinHelp(%q) = %q, want %q", tc.helps, got, tc.want)
		}
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
		"Never mind.":          "never_mind",
		"Cancel.":              "never_mind",
		"Say that again?":      "repeat",
		"Can you repeat that?": "repeat",
		"What can you do?":     "help",
	} {
		if rec, err := m.Recognize(context.Background(), transcript); err != nil || rec.Intent != want {
			t.Errorf("%q: %+v, %v; want %s", transcript, rec, err, want)
		}
	}
}
