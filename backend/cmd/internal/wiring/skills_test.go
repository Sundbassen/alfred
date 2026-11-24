package wiring

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2"

	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/voice/tts"
	"github.com/sundbassen/alfred/skills/music/musicservice"
)

func intents(t *testing.T, cfg Config) map[string]bool {
	t.Helper()
	r := assistant.NewRouter()
	if err := RegisterSkills(r, cfg, testOutput(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
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
	if got := intents(t, cfg); !got["music_play"] || !got["music_mix"] || !got["time"] {
		t.Errorf("logged in: intents %v, want clock and music", got)
	}
}

func TestAssistantWithoutModelUsesTemplates(t *testing.T) {
	r := assistant.NewRouter()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := Config{IntentModel: IntentModelConfig{Server: filepath.Join(t.TempDir(), "llama-server"), ModelFile: "missing.gguf"}}
	if err := RegisterSkills(r, cfg, testOutput(), logger); err != nil {
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
