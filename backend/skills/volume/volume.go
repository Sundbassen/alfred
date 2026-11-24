// Package volume is the volume skill: louder, quieter, a level, mute. It
// defines the Mixer interface it needs; volumeservice implements it for
// the speaker's audio output.
package volume

import (
	"context"
	"fmt"
	"math"
	"strings"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/spoken"
)

// Up is "volume up".
type Up struct{}

func (Up) IntentName() string { return "volume_up" }

// Down is "volume down".
type Down struct{}

func (Down) IntentName() string { return "volume_down" }

// Set is "set the volume to 5" (0–10, or a percentage).
type Set struct {
	Level string `slot:"level"`
}

func (Set) IntentName() string { return "volume_set" }

// Mute is "mute".
type Mute struct{}

func (Mute) IntentName() string { return "volume_mute" }

// Unmute is "unmute".
type Unmute struct{}

func (Unmute) IntentName() string { return "volume_unmute" }

// Mixer controls the speaker's output volume, a level from 0 to 1.
type Mixer interface {
	Volume(ctx context.Context) (level float64, muted bool, err error)
	SetVolume(ctx context.Context, level float64) error
	SetMute(ctx context.Context, muted bool) error
}

// step is how much louder or quieter "volume up/down" makes it.
const step = 0.1

// Register adds the volume intents to r.
func Register(r *assistant.Router, m Mixer) {
	h := &handler{mixer: m}
	assistant.Handle(r, assistant.Spec{
		Description: "Make the speaker louder",
		Phrases:     []string{"volume up", "louder", "turn it up", "turn the volume up", "increase the volume"},
		Help:        "change the volume",
	}, func(ctx context.Context, _ Up) (assistant.Response, error) { return h.change(ctx, step) })
	assistant.Handle(r, assistant.Spec{
		Description: "Make the speaker quieter",
		Phrases:     []string{"volume down", "quieter", "turn it down", "turn the volume down", "lower the volume"},
	}, func(ctx context.Context, _ Down) (assistant.Response, error) { return h.change(ctx, -step) })
	assistant.Handle(r, assistant.Spec{
		Description: "Set the speaker volume to a level from 0 to 10, or a percentage",
		Phrases:     []string{"set the volume to {level}", "set volume to {level}", "volume {level}", "volume to {level}"},
	}, h.set)
	assistant.Handle(r, assistant.Spec{
		Description: "Mute the speaker",
		Phrases:     []string{"mute", "mute the sound", "mute the speaker"},
	}, func(ctx context.Context, _ Mute) (assistant.Response, error) {
		return assistant.Response{}, h.mixer.SetMute(ctx, true)
	})
	assistant.Handle(r, assistant.Spec{
		Description: "Unmute the speaker",
		Phrases:     []string{"unmute", "unmute the sound", "unmute the speaker"},
	}, func(ctx context.Context, _ Unmute) (assistant.Response, error) {
		if err := h.mixer.SetMute(ctx, false); err != nil {
			return assistant.Response{}, err
		}
		return h.say(ctx)
	})
}

type handler struct {
	mixer Mixer
}

// change moves the volume by delta (and unmutes: louder while muted means
// "I want to hear it").
func (h *handler) change(ctx context.Context, delta float64) (assistant.Response, error) {
	level, muted, err := h.mixer.Volume(ctx)
	if err != nil {
		return assistant.Response{}, err
	}
	if err := h.setLevel(ctx, level+delta, muted); err != nil {
		return assistant.Response{}, err
	}
	return h.say(ctx)
}

func (h *handler) set(ctx context.Context, i Set) (assistant.Response, error) {
	level, ok := parseLevel(i.Level)
	if !ok {
		return assistant.Response{}, &assistant.SpokenError{
			Text: "Sorry, say a volume from 0 to 10.",
			Err:  errors.WithMessagef(fault.ErrInvalidValue, "volume level %q", i.Level),
		}
	}
	_, muted, err := h.mixer.Volume(ctx)
	if err != nil {
		return assistant.Response{}, err
	}
	if err := h.setLevel(ctx, level, muted); err != nil {
		return assistant.Response{}, err
	}
	return h.say(ctx)
}

func (h *handler) setLevel(ctx context.Context, level float64, muted bool) error {
	if err := h.mixer.SetVolume(ctx, min(max(level, 0), 1)); err != nil {
		return err
	}
	if muted {
		return h.mixer.SetMute(ctx, false)
	}
	return nil
}

// say answers with the volume as a level from 0 to 10.
func (h *handler) say(ctx context.Context) (assistant.Response, error) {
	level, _, err := h.mixer.Volume(ctx)
	if err != nil {
		return assistant.Response{}, err
	}
	return assistant.Response{Text: fmt.Sprintf("Volume %d.", int(math.Round(level*10)))}, nil
}

// parseLevel parses a spoken level: 0–10 ("5", "five"), a percentage ("50
// percent", or a number above 10), "max", "half".
func parseLevel(text string) (float64, bool) {
	tokens := strings.Fields(text)
	switch strings.Join(tokens, " ") {
	case "max", "maximum", "full", "100 percent":
		return 1, true
	case "half", "half way", "halfway":
		return 0.5, true
	case "min", "minimum", "zero", "0":
		return 0, true
	}
	n, next, ok := spoken.Number(tokens, 0)
	if !ok {
		return 0, false
	}
	percent := false
	if next < len(tokens) && (tokens[next] == "percent" || tokens[next] == "%") {
		percent = true
		next++
	}
	if next != len(tokens) || n > 100 {
		return 0, false
	}
	if percent || n > 10 {
		return float64(n) / 100, true
	}
	return float64(n) / 10, true
}
