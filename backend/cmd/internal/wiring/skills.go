// Package wiring builds Alfred's dependencies and registers the skills with
// them, in one place, for the binaries in cmd/. It is Alfred's composition
// root: every external implementation (provider) a skill uses is chosen
// here.
package wiring

import (
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/skills/clock"
)

// RegisterSkills constructs the skill providers and registers every skill
// on r. New skills and providers are added here, e.g.:
//
//	player := musicservice.NewSpotify(cfg.Spotify)
//	music.Register(r, player)
//
// The error is for providers that fail to construct (bad configuration, an
// unreachable service); none do yet.
func RegisterSkills(r *assistant.Router) error {
	clock.Register(r)
	return nil
}
