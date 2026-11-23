// Package wiring builds Alfred's dependencies and registers the skills with
// them, in one place, for the binaries in cmd/. It is Alfred's composition
// root: every external implementation (provider) a skill uses is chosen
// here.
package wiring

import (
	"log/slog"
	"os"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/skills/clock"
	"github.com/sundbassen/alfred/skills/music"
	"github.com/sundbassen/alfred/skills/music/musicservice"
)

// Config holds the skill providers' settings.
type Config struct {
	// Spotify for the music skill; an empty ClientID disables it.
	Spotify musicservice.SpotifyConfig
}

// ConfigFromEnv reads Config from the environment:
//
//	ALFRED_SPOTIFY_CLIENT_ID   Spotify app client ID (empty: no music skill)
//	ALFRED_SPOTIFY_TOKEN_FILE  login from make spotify-login (default ~/.config/alfred/spotify-token.json)
//	ALFRED_SPOTIFY_DEVICE      Spotify Connect name of this speaker (default "Alfred")
func ConfigFromEnv() Config {
	return Config{
		Spotify: musicservice.SpotifyConfig{
			ClientID:  os.Getenv("ALFRED_SPOTIFY_CLIENT_ID"),
			TokenFile: envOr("ALFRED_SPOTIFY_TOKEN_FILE", musicservice.DefaultSpotifyTokenFile()),
			Device:    envOr("ALFRED_SPOTIFY_DEVICE", "Alfred"),
		},
	}
}

// RegisterSkills constructs the skill providers and registers every skill
// on r. A skill whose provider is not configured, or not logged in, is left
// out with a log line, so Alfred still runs without it. Other provider
// failures are returned.
func RegisterSkills(r *assistant.Router, cfg Config, logger *slog.Logger) error {
	clock.Register(r)

	if err := registerMusic(r, cfg.Spotify, logger); err != nil {
		return err
	}
	return nil
}

// registerMusic registers the music skill with Spotify as its provider,
// unless Spotify is not configured or not logged in.
func registerMusic(r *assistant.Router, cfg musicservice.SpotifyConfig, logger *slog.Logger) error {
	if cfg.ClientID == "" {
		logger.Info("Music skill disabled: ALFRED_SPOTIFY_CLIENT_ID is not set")
		return nil
	}
	player, err := musicservice.NewSpotify(cfg)
	if errors.Is(err, fault.ErrUnauthorized) {
		logger.Warn("Music skill disabled: not logged in to Spotify (run make spotify-login)", slog.Any("error", err))
		return nil
	}
	if err != nil {
		return errors.WithMessage(err, "set up Spotify")
	}
	music.Register(r, player)
	logger.Info("Music skill enabled", slog.String("provider", "spotify"), slog.String("device", cfg.Device))
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
