package wiring

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/store"
	"github.com/sundbassen/alfred/internal/voice/tts"
	"github.com/sundbassen/alfred/skills/music/musicservice"
)

func intents(t *testing.T, cfg Config) map[string]bool {
	t.Helper()
	r := assistant.NewRouter()
	if err := RegisterSkills(context.Background(), r, cfg, testOutput(), testDB(t), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, rt := range r.Intents() {
		names[rt.Name] = true
	}
	return names
}

func TestMusicNeedsSpotifyConfigAndLogin(t *testing.T) {
	dir := t.TempDir()
	token := filepath.Join(dir, "token.json")

	if got := intents(t, Config{}); !got["time"] || got["music_play"] {
		t.Errorf("no client ID: intents %v, want clock only", got)
	}

	cfg := Config{Spotify: musicservice.SpotifyConfig{ClientID: "cid", TokenFile: token, Device: "Alfred"}}
	if got := intents(t, cfg); got["music_play"] {
		t.Errorf("not logged in: intents %v, want no music", got)
	}

	if err := musicservice.SaveToken(token, &oauth2.Token{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	if got := intents(t, cfg); !got["music_play"] || !got["music_mix"] || !got["time"] || !got["stop"] {
		t.Errorf("logged in: intents %v, want clock, music and stop", got)
	}

	// Bare "stop" goes to the stop skill (ringing timers first), not to
	// music_pause.
	r := assistant.NewRouter()
	if err := RegisterSkills(context.Background(), r, cfg, testOutput(), testDB(t), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if rec, err := match.New(r.Intents()).Recognize(context.Background(), "Stop."); err != nil || rec.Intent != "stop" {
		t.Errorf("\"Stop.\" = %+v, %v; want the stop intent", rec, err)
	}
}

func TestStopWithoutMusic(t *testing.T) {
	if got := intents(t, Config{}); !got["stop"] || !got["timer_set"] {
		t.Errorf("intents %v, want stop and timer_set without music", got)
	}
}

func TestAssistantWithoutModelUsesTemplates(t *testing.T) {
	r := assistant.NewRouter()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := Config{IntentModel: IntentModelConfig{Server: filepath.Join(t.TempDir(), "llama-server"), ModelFile: "missing.gguf"}}
	if err := RegisterSkills(context.Background(), r, cfg, testOutput(), testDB(t), logger); err != nil {
		t.Fatal(err)
	}
	a, err := NewAssistant(r, cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.WaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.Prompt == "" {
		t.Error("prompt is empty")
	}
	reply, err := a.Respond(context.Background(), "what time is it")
	if err != nil || reply.Recognition.By != "templates" {
		t.Errorf("Respond = %+v, %v; want a templates match", reply, err)
	}
}

func testOutput() *app.Output {
	return app.NewOutput(tts.NewLogSpeaker(slog.New(slog.DiscardHandler)), nil)
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "alfred.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ask recognizes a transcript with the templates and runs its handler.
func ask(t *testing.T, r *assistant.Router, transcript string) string {
	t.Helper()
	rec, err := match.New(r.Intents()).Recognize(context.Background(), transcript)
	if err != nil {
		t.Fatalf("%q: %v", transcript, err)
	}
	intent, err := r.New(rec.Intent)
	if err != nil {
		t.Fatal(err)
	}
	if intent, err = assistant.Fill(intent, rec.Slots); err != nil {
		t.Fatal(err)
	}
	resp, err := r.Dispatch(context.Background(), intent)
	if err != nil {
		return assistant.SpokenText(err)
	}
	return resp.Text
}

func TestTimersSurviveARestart(t *testing.T) {
	db := testDB(t)
	start := func() *assistant.Router {
		r := assistant.NewRouter()
		if err := RegisterSkills(context.Background(), r, Config{}, testOutput(), db, slog.New(slog.DiscardHandler)); err != nil {
			t.Fatal(err)
		}
		return r
	}

	if got := ask(t, start(), "Remind me to call my mom in 2 hours."); got != "OK, I'll remind you to call your mom in 2 hours." {
		t.Fatalf("set: %q", got)
	}
	// A new start over the same database brings the reminder back.
	r := start()
	if got := ask(t, r, "What reminders do I have?"); got != "Your reminder to call your mom is in 2 hours." {
		t.Errorf("after restart: %q", got)
	}
	if got := ask(t, r, "Cancel the reminder to call my mom."); got != "Cancelled your reminder to call your mom." {
		t.Errorf("cancel: %q", got)
	}
	if got := ask(t, start(), "How much time is left?"); got != "You have no timers, alarms or reminders." {
		t.Errorf("after cancel and restart: %q", got)
	}
}

func TestPromptCoversEveryIntent(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token.json")
	if err := musicservice.SaveToken(token, &oauth2.Token{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	r := assistant.NewRouter()
	cfg := Config{Spotify: musicservice.SpotifyConfig{ClientID: "cid", TokenFile: token}}
	if err := RegisterSkills(context.Background(), r, cfg, testOutput(), testDB(t), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	prompt := strings.ToLower(match.New(r.Intents()).Prompt())
	if words := len(strings.Fields(prompt)); words > match.MaxPromptWords {
		t.Errorf("prompt has %d words, want at most %d", words, match.MaxPromptWords)
	}
	var sentences []string
	for _, s := range strings.FieldsFunc(prompt, func(r rune) bool { return r == '.' || r == '?' }) {
		sentences = append(sentences, strings.TrimSpace(s))
	}
	// Every intent's first phrase that contributes anything is in the
	// prompt: a plain phrase as a sentence, a template fragment as a
	// sentence or the start of one.
	for _, rt := range r.Intents() {
		for _, phrase := range rt.Spec.Phrases {
			text := match.PromptText(phrase)
			if text == "" {
				continue
			}
			if !slices.ContainsFunc(sentences, func(s string) bool {
				return s == text || strings.Contains(phrase, "{") && strings.HasPrefix(s, text+" ")
			}) {
				t.Errorf("prompt lacks %s's %q:\n%s", rt.Name, text, prompt)
			}
			break
		}
	}
	t.Logf("%d words: %s", len(strings.Fields(prompt)), prompt)
}

func TestHelpAndRepeat(t *testing.T) {
	r := assistant.NewRouter()
	if err := RegisterSkills(context.Background(), r, Config{}, testOutput(), testDB(t), slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	want := "I can tell you the time and date, and set timers and reminders."
	if got := ask(t, r, "What can you do?"); got != want {
		t.Errorf("help: %q, want %q", got, want)
	}
	if got := ask(t, r, "Say that again."); got != want {
		t.Errorf("repeat: %q, want the help again", got)
	}
}
