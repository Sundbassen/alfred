package wiring

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/skills/music/musicservice"
)

func intents(t *testing.T, cfg Config) map[string]bool {
	t.Helper()
	r := assistant.NewRouter()
	if err := RegisterSkills(r, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
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
