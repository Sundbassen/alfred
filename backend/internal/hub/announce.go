package hub

import (
	"context"
	"log/slog"
	"sync"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

// Chime plays the chime on every connected satellite and waits until they
// have played it. With the hub as the clock skill's Announcer, timers ring
// on the satellites. (Ringing only on the satellite that set the timer
// comes with per-device sessions, see the full hub plan.)
func (h *Hub) Chime(ctx context.Context) error {
	return h.announceAll(ctx, h.chime, "")
}

// Speak says text on every connected satellite.
func (h *Hub) Speak(ctx context.Context, text string) error {
	wav, err := h.cfg.Synthesizer.Synthesize(ctx, text)
	if err != nil {
		return err
	}
	return h.announceAll(ctx, h.media.put(wav, false), text)
}

func (h *Hub) announceAll(ctx context.Context, mediaID, text string) error {
	sessions := h.connected()
	if len(sessions) == 0 {
		h.logger.Warn("No satellite connected to announce on", slog.String("text", text))
		return errors.WithMessage(fault.ErrServiceUnavailable, "no satellite connected")
	}
	var wg sync.WaitGroup
	errs := make([]error, len(sessions))
	for i, s := range sessions {
		wg.Go(func() { errs[i] = s.announce(ctx, mediaID, text) })
	}
	wg.Wait()
	return errors.Join(errs...)
}
