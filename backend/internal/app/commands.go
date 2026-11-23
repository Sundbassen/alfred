package app

import (
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/commands/clock"
)

// Router returns the router with everything Alfred can do. This is the one
// place commands are registered: add new ones here, passing in the
// services they need (e.g. lights.Register(r, services.HomeAssistant)).
func Router() *assistant.Router {
	r := assistant.NewRouter()
	r.Use(assistant.SpeakErrors)

	clock.Register(r)

	return r
}
