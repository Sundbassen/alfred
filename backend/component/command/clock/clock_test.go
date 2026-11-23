package clock

import (
	"context"
	"testing"
	"time"

	"github.com/sundbassen/alfred/component/command"
)

func TestExecute(t *testing.T) {
	fixed := time.Date(2026, 11, 23, 9, 5, 0, 0, time.Local)
	cmd := newWithNow(func() time.Time { return fixed })

	resp, err := cmd.Execute(context.Background(), command.Request{Transcript: "what time is it"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "It's 09:05."; resp.Text != want {
		t.Errorf("Text = %q, want %q", resp.Text, want)
	}
}

func TestPhrasesMatchInRegistry(t *testing.T) {
	registry := command.NewRegistry(New())
	for _, transcript := range []string{
		" What time is it?",
		"Hey Alfred, what's the time?",
		"Tell me the time, please.",
	} {
		match, ok := registry.Match(transcript)
		if !ok || match.Command.ID() != "time" {
			t.Errorf("Match(%q) = %+v, %v; want time command", transcript, match, ok)
		}
	}
}
