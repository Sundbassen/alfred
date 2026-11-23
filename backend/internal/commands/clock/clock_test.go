package clock

import (
	"context"
	"testing"
	"time"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
)

func TestAskTime(t *testing.T) {
	r := assistant.NewRouter()
	register(r, func() time.Time { return time.Date(2026, 11, 23, 9, 5, 0, 0, time.Local) })

	resp, err := r.Dispatch(context.Background(), AskTime{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "It's 09:05."; resp.Text != want {
		t.Errorf("Text = %q, want %q", resp.Text, want)
	}
}

func TestPhrasesAreRecognized(t *testing.T) {
	r := assistant.NewRouter()
	Register(r)
	m := match.New(r.Intents())
	for _, transcript := range []string{
		" What time is it?",
		"Hey Alfred, what's the time?",
		"Tell me the time, please.",
	} {
		res, err := m.Recognize(transcript)
		if err != nil || res.Name != (AskTime{}).IntentName() {
			t.Errorf("Recognize(%q) = %+v, %v; want time", transcript, res, err)
		}
	}
}
