package app

import (
	"context"
	"sync"

	"github.com/sundbassen/alfred/internal/voice/sound"
	"github.com/sundbassen/alfred/internal/voice/tts"
)

// Output is Alfred's audio output: speech and the chime. It plays one thing
// at a time, so a ringing timer never talks over a reply (or another
// timer). Skills that speak on their own (timers) receive it from wiring.
type Output struct {
	mu      sync.Mutex
	speaker tts.Speaker
	player  *sound.Player // nil: no chime
	chime   []byte
}

// NewOutput returns an Output speaking with speaker and chiming with
// player; a nil player leaves the chime out (e.g. without paplay).
func NewOutput(speaker tts.Speaker, player *sound.Player) *Output {
	return &Output{speaker: speaker, player: player, chime: sound.Chime()}
}

// Speak says text, after whatever is playing finishes.
func (o *Output) Speak(ctx context.Context, text string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.speaker.Speak(ctx, text)
}

// Chime plays the chime, after whatever is playing finishes.
func (o *Output) Chime(ctx context.Context) error {
	if o.player == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.player.Play(ctx, o.chime)
}
