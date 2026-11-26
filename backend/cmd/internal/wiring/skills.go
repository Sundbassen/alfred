// Package wiring builds Alfred's dependencies and registers the skills with
// them, in one place, for the binaries in cmd/. It is Alfred's composition
// root: every external implementation (provider) a skill uses is chosen
// here.
package wiring

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/llm/ministral"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/store"
	"github.com/sundbassen/alfred/skills/clock"
	"github.com/sundbassen/alfred/skills/clock/clockstore"
	"github.com/sundbassen/alfred/skills/conversation"
	"github.com/sundbassen/alfred/skills/music"
	"github.com/sundbassen/alfred/skills/music/musicservice"
	"github.com/sundbassen/alfred/skills/stop"
	"github.com/sundbassen/alfred/skills/volume"
	"github.com/sundbassen/alfred/skills/volume/volumeservice"
)

// Config holds the skill providers' and recognizers' settings.
type Config struct {
	// DB is the local database file (internal/store), e.g. for timers.
	DB string
	// Spotify for the music skill; an empty ClientID disables it.
	Spotify musicservice.SpotifyConfig
	// Volume for the volume skill; an empty Path (no wpctl) disables it.
	Volume volumeservice.WpctlConfig
	// IntentModel is the local model recognizing what the phrase
	// templates miss; a missing model file disables it.
	IntentModel IntentModelConfig
}

// IntentModelConfig configures the local intent model (llama-server).
type IntentModelConfig struct {
	Server    string        // llama-server binary
	ModelFile string        // GGUF model file
	Threads   int           // CPU threads for the model
	Timeout   time.Duration // per recognition; slower answers fall through
}

// ConfigFromEnv reads Config from the environment:
//
//	ALFRED_DB                  local database (default $XDG_STATE_HOME/alfred/alfred.db, i.e. ~/.local/state/alfred/alfred.db)
//	ALFRED_SPOTIFY_CLIENT_ID   Spotify app client ID (empty: no music skill)
//	ALFRED_SPOTIFY_TOKEN_FILE  login from make spotify-login (default ~/.config/alfred/spotify-token.json)
//	ALFRED_SPOTIFY_DEVICE      Spotify Connect name of this speaker (default "Alfred")
//	ALFRED_WPCTL               wpctl binary for the volume skill (default: wpctl on PATH; none: no volume skill)
//	ALFRED_LLAMA_SERVER        llama-server binary (default .deps/llama.cpp/build-server/bin/llama-server; set by make)
//	ALFRED_INTENT_MODEL_FILE   local intent model (default models/llm/<Ministral GGUF>; missing: no model tier)
//	ALFRED_INTENT_MODEL_THREADS  CPU threads for the model (default 4)
//	ALFRED_INTENT_MODEL_TIMEOUT  per recognition, e.g. "3s" (default 3s)
func ConfigFromEnv() Config {
	return Config{
		DB: envOr("ALFRED_DB", store.DefaultPath()),
		Spotify: musicservice.SpotifyConfig{
			ClientID:  os.Getenv("ALFRED_SPOTIFY_CLIENT_ID"),
			TokenFile: envOr("ALFRED_SPOTIFY_TOKEN_FILE", musicservice.DefaultSpotifyTokenFile()),
			Device:    envOr("ALFRED_SPOTIFY_DEVICE", "Alfred"),
		},
		Volume: volumeservice.WpctlConfig{Path: envOr("ALFRED_WPCTL", volumeservice.WpctlPath())},
		IntentModel: IntentModelConfig{
			Server:    envOr("ALFRED_LLAMA_SERVER", ".deps/llama.cpp/build-server/bin/llama-server"),
			ModelFile: envOr("ALFRED_INTENT_MODEL_FILE", filepath.Join("models", "llm", ministral.ModelFile)),
			Threads:   envInt("ALFRED_INTENT_MODEL_THREADS", 4),
			Timeout:   envDuration("ALFRED_INTENT_MODEL_TIMEOUT", 3*time.Second),
		},
	}
}

// RegisterSkills constructs the skill providers and registers every skill
// on r. Skills that speak on their own (timers) do so through out; skills
// that keep state across restarts keep it in db (store.Open). A skill whose
// provider is not configured, or not logged in, is left out with a log
// line, so Alfred still runs without it. Other provider failures, and a
// failure to restore saved state, are returned.
func RegisterSkills(ctx context.Context, r *assistant.Router, cfg Config, out *app.Output, db *sql.DB, logger *slog.Logger) error {
	// Remembers the last reply, for "repeat that".
	history := conversation.NewHistory()
	r.Use(history.Middleware)
	conversation.Register(r, history)

	timers := clock.NewTimers(out, clockstore.New(db), logger)
	if err := timers.Restore(ctx); err != nil {
		return err
	}
	clock.Register(r, timers)

	registerVolume(r, cfg.Volume, logger)

	player, err := registerMusic(r, cfg.Spotify, logger)
	if err != nil {
		return err
	}

	// "stop" stops the most urgent thing first: a ringing timer, then the
	// music.
	stoppers := []stop.Stopper{timers}
	if player != nil {
		stoppers = append(stoppers, stop.StopperFunc(func(ctx context.Context) (bool, error) {
			return true, player.Pause(ctx, music.Target{})
		}))
	}
	stop.Register(r, stoppers...)
	return nil
}

// registerVolume registers the volume skill on the default PipeWire output,
// unless wpctl is missing.
func registerVolume(r *assistant.Router, cfg volumeservice.WpctlConfig, logger *slog.Logger) {
	if cfg.Path == "" {
		logger.Info("Volume skill disabled: wpctl not found (install WirePlumber, or set ALFRED_WPCTL)")
		return
	}
	volume.Register(r, volumeservice.NewWpctl(cfg))
	logger.Info("Volume skill enabled", slog.String("provider", "wpctl"), slog.String("path", cfg.Path))
}

// registerMusic registers the music skill with Spotify as its provider,
// unless Spotify is not configured or not logged in (then the player is
// nil).
func registerMusic(r *assistant.Router, cfg musicservice.SpotifyConfig, logger *slog.Logger) (music.Player, error) {
	if cfg.ClientID == "" {
		logger.Info("Music skill disabled: ALFRED_SPOTIFY_CLIENT_ID is not set")
		return nil, nil
	}
	player, err := musicservice.NewSpotify(cfg)
	if errors.Is(err, fault.ErrUnauthorized) {
		logger.Warn("Music skill disabled: not logged in to Spotify (run make spotify-login)", slog.Any("error", err))
		return nil, nil
	}
	if err != nil {
		return nil, errors.WithMessage(err, "set up Spotify")
	}
	music.Register(r, player)
	logger.Info("Music skill enabled", slog.String("provider", "spotify"), slog.String("device", cfg.Device))
	return player, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil && d > 0 {
		return d
	}
	return def
}
