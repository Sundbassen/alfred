package wiring

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
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
	"github.com/sundbassen/alfred/skills/volume/volumeservice"
	"github.com/sundbassen/alfred/skills/weather/weatherservice"
)

func intents(t *testing.T, cfg Config) map[string]bool {
	t.Helper()
	r := assistant.NewRouter()
	if err := RegisterSkills(context.Background(), r, cfg, Deps{Output: testOutput(), DB: testDB(t)}, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
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
	if err := RegisterSkills(context.Background(), r, cfg, Deps{Output: testOutput(), DB: testDB(t)}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if rec, err := match.New(r.Intents()).Recognize(context.Background(), "Stop."); err != nil || rec.Intent != "stop" {
		t.Errorf("\"Stop.\" = %+v, %v; want the stop intent", rec, err)
	}
}

// fakeWpctl is an executable to configure as wpctl; registering the volume
// skill only checks that it exists.
func fakeWpctl(t *testing.T) volumeservice.WpctlConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wpctl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return volumeservice.WpctlConfig{Path: path}
}

func TestVolumeNeedsWpctl(t *testing.T) {
	if got := intents(t, Config{}); got["volume_up"] {
		t.Error("volume registered without wpctl")
	}
	if got := intents(t, Config{Volume: volumeservice.WpctlConfig{Path: filepath.Join(t.TempDir(), "missing")}}); got["volume_up"] {
		t.Error("volume registered with a missing wpctl")
	}
	if got := intents(t, Config{Volume: fakeWpctl(t)}); !got["volume_up"] {
		t.Error("volume not registered with wpctl")
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
	if err := RegisterSkills(context.Background(), r, cfg, Deps{Output: testOutput(), DB: testDB(t)}, logger); err != nil {
		t.Fatal(err)
	}
	a, err := NewAssistant(r, cfg, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
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
		if err := RegisterSkills(context.Background(), r, Config{}, Deps{Output: testOutput(), DB: db}, slog.New(slog.DiscardHandler)); err != nil {
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
	// Every skill, whatever this machine has installed.
	cfg := Config{
		Spotify: musicservice.SpotifyConfig{ClientID: "cid", TokenFile: token},
		Volume:  fakeWpctl(t),
		Weather: &weatherservice.OpenMeteoConfig{Latitude: 55.7, Longitude: 12.6},
	}
	if err := RegisterSkills(context.Background(), r, cfg, Deps{Output: testOutput(), DB: testDB(t)}, slog.New(slog.DiscardHandler)); err != nil {
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
	cfg := Config{Volume: fakeWpctl(t)}
	if err := RegisterSkills(context.Background(), r, cfg, Deps{Output: testOutput(), DB: testDB(t)}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	want := "I can tell you the time and date, set timers and reminders, change the volume, and keep lists."
	if got := ask(t, r, "What can you do?"); got != want {
		t.Errorf("help: %q, want %q", got, want)
	}
	if got := ask(t, r, "Say that again."); got != want {
		t.Errorf("repeat: %q, want the help again", got)
	}
}

func TestFollowUpQuestion(t *testing.T) {
	r := assistant.NewRouter()
	logger := slog.New(slog.DiscardHandler)
	cfg := Config{IntentModel: IntentModelConfig{Server: filepath.Join(t.TempDir(), "llama-server"), ModelFile: "missing.gguf"}}
	if err := RegisterSkills(context.Background(), r, cfg, Deps{Output: testOutput(), DB: testDB(t)}, logger); err != nil {
		t.Fatal(err)
	}
	a, err := NewAssistant(r, cfg, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	say := func(transcript string) *app.Reply {
		t.Helper()
		reply, err := a.Respond(context.Background(), transcript)
		if err != nil {
			t.Fatalf("%q: %v", transcript, err)
		}
		return reply
	}
	say("Set a timer for 10 minutes.")
	say("Set a timer for 1 hour.")
	if reply := say("Cancel the timer."); !reply.Listen ||
		reply.Text != "You have 2: your timer for 10 minutes and your timer for 1 hour. Which one?" {
		t.Fatalf("ambiguous cancel: %+v", reply)
	}
	if reply := say("The 10 minute one."); reply.Text != "Cancelled your timer for 10 minutes." {
		t.Errorf("answer: %+v", reply)
	}
	if reply := say("How much time is left?"); reply.Text != "Your timer for 1 hour has 1 hour left." {
		t.Errorf("left: %+v", reply)
	}
}

func TestSleepTimerPhrasesBeatClockCancel(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token.json")
	if err := musicservice.SaveToken(token, &oauth2.Token{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	r := assistant.NewRouter()
	cfg := Config{Spotify: musicservice.SpotifyConfig{ClientID: "cid", TokenFile: token}}
	if err := RegisterSkills(context.Background(), r, cfg, Deps{Output: testOutput(), DB: testDB(t)}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	m := match.New(r.Intents())
	for transcript, want := range map[string]string{
		"Cancel the sleep timer.":           "music_sleep_cancel",
		"Cancel the timer.":                 "timer_cancel",
		"Add 5 minutes to the timer.":       "timer_extend",
		"Stop the music in 20 minutes.":     "music_sleep",
		"Set a sleep timer for 30 minutes.": "music_sleep",
		"Set a timer for 30 minutes.":       "timer_set",
	} {
		if rec, err := m.Recognize(context.Background(), transcript); err != nil || rec.Intent != want {
			t.Errorf("%q: %+v, %v; want %s", transcript, rec, err, want)
		}
	}
}

func TestListsEndToEnd(t *testing.T) {
	r := assistant.NewRouter()
	logger := slog.New(slog.DiscardHandler)
	cfg := Config{IntentModel: IntentModelConfig{Server: filepath.Join(t.TempDir(), "llama-server"), ModelFile: "missing.gguf"}}
	db := testDB(t)
	if err := RegisterSkills(context.Background(), r, cfg, Deps{Output: testOutput(), DB: db}, logger); err != nil {
		t.Fatal(err)
	}
	a, err := NewAssistant(r, cfg, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range [][2]string{
		{"Add milk, eggs and bread to the shopping list.", "You don't have a list called shopping. Should I create it?"},
		{"Yes.", "Added milk, eggs and bread to your shopping list."},
		// Timer phrases still win over "add {items} to the {list}".
		{"Set a timer for 10 minutes.", "Timer set for 10 minutes."},
		{"Add 5 minutes to the timer.", "Your timer now has 15 minutes left."},
		{"What's on my shopping list?", "Your shopping list has 3 items: milk, eggs and bread."},
		{"Delete the shopping list.", "Delete your shopping list and its 3 items?"},
		{"Never mind.", ""},
		{"What lists do I have?", "You have one list: shopping."},
	} {
		reply, err := a.Respond(context.Background(), step[0])
		if err != nil {
			t.Fatalf("%q: %v", step[0], err)
		}
		if reply.Text != step[1] {
			t.Errorf("%q:\n got %q\nwant %q", step[0], reply.Text, step[1])
		}
	}
}

func TestListPhrasesDontCatchOtherCommands(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token.json")
	if err := musicservice.SaveToken(token, &oauth2.Token{AccessToken: "a", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	r := assistant.NewRouter()
	cfg := Config{Spotify: musicservice.SpotifyConfig{ClientID: "cid", TokenFile: token}, Volume: fakeWpctl(t)}
	if err := RegisterSkills(context.Background(), r, cfg, Deps{Output: testOutput(), DB: testDB(t)}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	m := match.New(r.Intents())
	// Commands for other skills, which short list templates ("add {items}",
	// "read the {list}") used to take as list commands.
	for _, transcript := range []string{
		"Add 5 minutes.", "Add 5 minutes to timer.", "Add an alarm for 7 am.", "Remove the alarm.",
		"Read the news.", "Put daft punk on the kitchen speaker.",
	} {
		if rec, err := m.Recognize(context.Background(), transcript); err == nil && strings.HasPrefix(rec.Intent, "list_") {
			t.Errorf("%q went to %s (%q)", transcript, rec.Intent, rec.Phrase)
		}
	}
	// Known limit of the fuzzy tier: one word off a phrase still scores 0.8
	// ("what is on the radio" against "what is on the list"). It is no
	// longer a full template match, at least.
	if rec, err := m.Recognize(context.Background(), "What's on the radio?"); err == nil && rec.Score >= 1 {
		t.Errorf("what's on the radio: full match %+v", rec)
	}
	for transcript, want := range map[string]string{
		"Add milk to the shopping list.":      "list_add",
		"Add milk to shopping list.":          "list_add",
		"Add milk to it.":                     "list_add",
		"Remove milk from the shopping list.": "list_remove",
		"What's on my shopping list?":         "list_read",
		"Read the list.":                      "list_read",
		"Clear the packing list.":             "list_clear",
		"Delete the list.":                    "list_delete",
		"Add 5 minutes to the timer.":         "timer_extend",
	} {
		if rec, err := m.Recognize(context.Background(), transcript); err != nil || rec.Intent != want {
			t.Errorf("%q: %+v, %v; want %s", transcript, rec, err, want)
		}
	}
}

func TestWeatherNeedsALocation(t *testing.T) {
	if got := intents(t, Config{}); got["weather"] {
		t.Error("weather registered without a location")
	}
	if got := intents(t, Config{Weather: &weatherservice.OpenMeteoConfig{Latitude: 55.7, Longitude: 12.6}}); !got["weather"] || !got["weather_rain"] {
		t.Errorf("intents %v, want weather with a location", got)
	}
	t.Setenv("ALFRED_WEATHER_LATITUDE", "55.6761")
	t.Setenv("ALFRED_WEATHER_LONGITUDE", "not a number")
	if cfg := ConfigFromEnv(); cfg.Weather != nil {
		t.Errorf("invalid longitude: %+v", cfg.Weather)
	}
	t.Setenv("ALFRED_WEATHER_LONGITUDE", "12.5683")
	t.Setenv("ALFRED_WEATHER_PLACE", "Copenhagen")
	if cfg := ConfigFromEnv(); cfg.Weather == nil || cfg.WeatherPlace != "Copenhagen" || cfg.Weather.Longitude != 12.5683 {
		t.Errorf("weather config: %+v, place %q", cfg.Weather, cfg.WeatherPlace)
	}
	t.Setenv("ALFRED_WEATHER_LATITUDE", "NaN")
	if cfg := ConfigFromEnv(); cfg.Weather != nil {
		t.Errorf("NaN latitude: %+v", cfg.Weather)
	}
}

func TestNoModelNoAnswers(t *testing.T) {
	cfg := Config{IntentModel: IntentModelConfig{Server: filepath.Join(t.TempDir(), "llama-server"), ModelFile: "missing.gguf"}}
	model, err := StartModel(cfg, slog.New(slog.DiscardHandler))
	if err != nil || model != nil {
		t.Fatalf("StartModel without a model = %v, %v; want nil, nil", model, err)
	}
	model.Close() // nil is fine
	if got := intents(t, Config{}); got["answer"] {
		t.Error("answer skill registered without a model")
	}
}
