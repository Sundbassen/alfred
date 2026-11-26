// Package music is the music skill: play, pause, resume, skip, and a mix of
// artists. It defines the Player interface it needs; providers (e.g.
// Spotify) implement it in music/musicservice and are passed to Register by
// cmd/internal/wiring.
package music

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/spoken"
)

// Play is "play daft punk": search for something and play it.
type Play struct {
	Query string `slot:"query"`
}

func (Play) IntentName() string { return "music_play" }

// Mix is "play a radio with daft punk and justice": a shuffled mix of the
// named artists only (Spotify offers no recommendations to new apps).
type Mix struct {
	Artists string `slot:"artists"`
}

func (Mix) IntentName() string { return "music_mix" }

// Pause stops playback.
type Pause struct{}

func (Pause) IntentName() string { return "music_pause" }

// Resume continues playback.
type Resume struct{}

func (Resume) IntentName() string { return "music_resume" }

// Next skips to the next track.
type Next struct{}

func (Next) IntentName() string { return "music_next" }

// Previous goes back to the previous track.
type Previous struct{}

func (Previous) IntentName() string { return "music_previous" }

// NowPlaying is "what's playing".
type NowPlaying struct{}

func (NowPlaying) IntentName() string { return "music_now" }

// Shuffle is "shuffle" / "shuffle on".
type Shuffle struct{}

func (Shuffle) IntentName() string { return "music_shuffle" }

// NoShuffle is "shuffle off".
type NoShuffle struct{}

func (NoShuffle) IntentName() string { return "music_no_shuffle" }

// Sleep is "stop the music in 30 minutes": a sleep timer.
type Sleep struct {
	When string `slot:"when"`
}

func (Sleep) IntentName() string { return "music_sleep" }

// CancelSleep is "cancel the sleep timer".
type CancelSleep struct{}

func (CancelSleep) IntentName() string { return "music_sleep_cancel" }

// Target says where to play. The zero value means this speaker. Later,
// with several Alfred speakers in a home, Speaker names another one
// ("kitchen"), resolved through the home registry.
type Target struct {
	Speaker string
}

// Item is what started playing.
type Item struct {
	Title  string
	Artist string // empty for e.g. a playlist
}

// Player is what the music skill needs from a provider. Errors are fault
// categories; fault.ErrNotFound from Play or PlayMix means the query or an
// artist was not found.
type Player interface {
	// Play searches for query and starts the best match.
	Play(ctx context.Context, t Target, query string) (Item, error)
	// PlayMix plays a shuffled mix of the artists' tracks and returns the
	// artist names as the provider resolved them.
	PlayMix(ctx context.Context, t Target, artists []string) ([]string, error)
	Pause(ctx context.Context, t Target) error
	Resume(ctx context.Context, t Target) error
	Next(ctx context.Context, t Target) error
	Previous(ctx context.Context, t Target) error
	// NowPlaying returns what is playing; fault.ErrNotFound if nothing.
	NowPlaying(ctx context.Context, t Target) (Item, error)
	SetShuffle(ctx context.Context, t Target, on bool) error
}

// maxMixArtists bounds a mix; more names are likely a misrecognition.
const maxMixArtists = 5

// Register adds the music intents to r, played by p. logger reports what
// happens in the background (the sleep timer).
func Register(r *assistant.Router, p Player, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	h := &handler{player: p, logger: logger, now: time.Now}

	assistant.Handle(r, assistant.Spec{
		Description: "Search for music and play it",
		Help:        "play music on Spotify",
		Phrases:     []string{"play {query}", "put on {query}", "play some {query}"},
	}, h.play)
	assistant.Handle(r, assistant.Spec{
		Description: "Play a shuffled mix of the named artists",
		Phrases: []string{
			"play a radio with {artists}", "play a radio of {artists}",
			"play a mix of {artists}", "play a mix with {artists}", "radio with {artists}",
		},
	}, h.mix)
	assistant.Handle(r, assistant.Spec{
		Description: "Pause the music",
		// Bare "stop" is the stop skill's: it stops a ringing timer first.
		Phrases: []string{"pause", "pause the music", "stop the music"},
	}, h.pause)
	assistant.Handle(r, assistant.Spec{
		Description: "Resume the music",
		Phrases:     []string{"play", "resume", "resume the music", "continue the music", "unpause"},
	}, h.resume)
	assistant.Handle(r, assistant.Spec{
		Description: "Skip to the next track",
		Phrases:     []string{"next", "next song", "next track", "skip", "skip this song"},
	}, h.next)
	assistant.Handle(r, assistant.Spec{
		Description: "Go back to the previous track",
		Phrases:     []string{"previous", "previous song", "previous track", "go back", "last song"},
	}, h.previous)
	assistant.Handle(r, assistant.Spec{
		Description: "Say which song is playing",
		Phrases:     []string{"what is playing", "what song is this", "what is this song", "who is this", "what am i listening to"},
	}, h.nowPlaying)
	assistant.Handle(r, assistant.Spec{
		Description: "Turn shuffle on",
		Phrases:     []string{"shuffle", "shuffle on", "turn on shuffle", "shuffle the music"},
	}, h.shuffle)
	assistant.Handle(r, assistant.Spec{
		Description: "Turn shuffle off",
		Phrases:     []string{"shuffle off", "turn off shuffle", "stop shuffling"},
	}, h.noShuffle)
	assistant.Handle(r, assistant.Spec{
		Description: "Stop the music after a while or at a time: a sleep timer",
		Phrases: []string{
			"stop the music in {when}", "pause the music in {when}", "turn off the music in {when}",
			"stop the music at {when}", "sleep timer {when}",
			"set a sleep timer for {when}", "sleep timer for {when}",
		},
	}, h.sleep)
	assistant.Handle(r, assistant.Spec{
		Description: "Cancel the sleep timer",
		Phrases:     []string{"cancel the sleep timer", "turn off the sleep timer", "no sleep timer"},
	}, h.cancelSleep)
}

type handler struct {
	player Player
	logger *slog.Logger
	now    func() time.Time

	mu         sync.Mutex
	sleepTimer *time.Timer // the pending sleep timer, if any
}

func (h *handler) nowPlaying(ctx context.Context, _ NowPlaying) (assistant.Response, error) {
	item, err := h.player.NowPlaying(ctx, Target{})
	if errors.Is(err, fault.ErrNotFound) {
		return assistant.Response{Text: "Nothing is playing."}, nil
	}
	if err != nil {
		return assistant.Response{}, err
	}
	if item.Artist == "" {
		return assistant.Response{Text: "This is " + item.Title + "."}, nil
	}
	return assistant.Response{Text: "This is " + item.Title + " by " + item.Artist + "."}, nil
}

// sleep pauses the music after the given time. It is kept in memory only:
// the music doesn't resume after a restart either, and at shutdown it just
// goes with the process. A new one replaces the pending one; pausing the
// music cancels it.
func (h *handler) sleep(ctx context.Context, in Sleep) (assistant.Response, error) {
	now := h.now()
	w, err := spoken.ParseWhen(in.When, now)
	if err != nil {
		return assistant.Response{}, err
	}
	h.mu.Lock()
	if h.sleepTimer != nil {
		h.sleepTimer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(w.At.Sub(now), func() {
		h.mu.Lock()
		if h.sleepTimer != timer { // replaced or cancelled as it fired
			h.mu.Unlock()
			return
		}
		h.sleepTimer = nil
		h.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.player.Pause(ctx, Target{}); err != nil {
			h.logger.Warn("Sleep timer could not pause the music", slog.String("error", err.Error()))
			return
		}
		h.logger.Info("Sleep timer paused the music")
	})
	h.sleepTimer = timer
	h.mu.Unlock()
	if w.Duration > 0 {
		return assistant.Response{Text: "OK, I'll stop the music in " + spoken.FormatDuration(w.Duration) + "."}, nil
	}
	return assistant.Response{Text: "OK, I'll stop the music at " + spoken.FormatClock(w.At, now) + "."}, nil
}

func (h *handler) play(ctx context.Context, in Play) (assistant.Response, error) {
	item, err := h.player.Play(ctx, Target{}, in.Query)
	if errors.Is(err, fault.ErrNotFound) {
		return assistant.Response{}, &assistant.SpokenError{Text: "I couldn't find anything called " + in.Query + ".", Err: err}
	}
	if err != nil {
		return assistant.Response{}, err
	}
	if item.Artist == "" {
		return assistant.Response{Text: "Playing " + item.Title + "."}, nil
	}
	return assistant.Response{Text: "Playing " + item.Title + " by " + item.Artist + "."}, nil
}

func (h *handler) mix(ctx context.Context, in Mix) (assistant.Response, error) {
	artists := splitArtists(in.Artists)
	if len(artists) == 0 || len(artists) > maxMixArtists {
		return assistant.Response{}, &assistant.SpokenError{
			Text: "Please name one to five artists for a mix.",
			Err:  errors.WithMessagef(fault.ErrInvalidValue, "mix of %d artists", len(artists)),
		}
	}
	resolved, err := h.player.PlayMix(ctx, Target{}, artists)
	if errors.Is(err, fault.ErrNotFound) {
		return assistant.Response{}, &assistant.SpokenError{Text: "I couldn't find all of " + joinNames(artists) + ".", Err: err}
	}
	if err != nil {
		return assistant.Response{}, err
	}
	return assistant.Response{Text: "Playing a mix of " + joinNames(resolved) + "."}, nil
}

// stopSleep cancels the pending sleep timer, reporting whether there was
// one.
func (h *handler) stopSleep() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sleepTimer == nil {
		return false
	}
	h.sleepTimer.Stop()
	h.sleepTimer = nil
	return true
}

func (h *handler) cancelSleep(context.Context, CancelSleep) (assistant.Response, error) {
	if !h.stopSleep() {
		return assistant.Response{Text: "There is no sleep timer."}, nil
	}
	return assistant.Response{Text: "Sleep timer cancelled."}, nil
}

func (h *handler) pause(ctx context.Context, _ Pause) (assistant.Response, error) {
	if err := h.player.Pause(ctx, Target{}); err != nil {
		return assistant.Response{}, err
	}
	h.stopSleep()
	return assistant.Response{Text: "Paused."}, nil
}

func (h *handler) shuffle(ctx context.Context, _ Shuffle) (assistant.Response, error) {
	return ack("Shuffle on.", h.player.SetShuffle(ctx, Target{}, true))
}

func (h *handler) noShuffle(ctx context.Context, _ NoShuffle) (assistant.Response, error) {
	return ack("Shuffle off.", h.player.SetShuffle(ctx, Target{}, false))
}

func (h *handler) resume(ctx context.Context, _ Resume) (assistant.Response, error) {
	return ack("Resuming.", h.player.Resume(ctx, Target{}))
}

func (h *handler) next(ctx context.Context, _ Next) (assistant.Response, error) {
	return ack("Skipping.", h.player.Next(ctx, Target{}))
}

func (h *handler) previous(ctx context.Context, _ Previous) (assistant.Response, error) {
	return ack("Going back.", h.player.Previous(ctx, Target{}))
}

func ack(text string, err error) (assistant.Response, error) {
	if err != nil {
		return assistant.Response{}, err
	}
	return assistant.Response{Text: text}, nil
}

// splitArtists splits "daft punk and justice" into its artists. The
// transcript is normalized, so punctuation (commas) is already gone: "and"
// is the separator.
func splitArtists(s string) []string {
	var artists []string
	for _, a := range strings.Split(s, " and ") {
		if a = strings.TrimSpace(a); a != "" {
			artists = append(artists, a)
		}
	}
	return artists
}

// joinNames joins names as in a sentence: "A", "A and B", "A, B and C".
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
