// Package music is the music skill: play, pause, resume, skip, and a mix of
// artists. It defines the Player interface it needs; providers (e.g.
// Spotify) implement it in music/musicservice and are passed to Register by
// cmd/internal/wiring.
package music

import (
	"context"
	"strings"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/platform/fault"
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
}

// maxMixArtists bounds a mix; more names are likely a misrecognition.
const maxMixArtists = 5

// Register adds the music intents to r, played by p.
func Register(r *assistant.Router, p Player) {
	h := &handler{player: p}

	assistant.Handle(r, assistant.Spec{
		Description: "Search for music and play it",
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
		Phrases:     []string{"pause", "pause the music", "stop", "stop the music"},
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
}

type handler struct {
	player Player
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

func (h *handler) pause(ctx context.Context, _ Pause) (assistant.Response, error) {
	return ack("Paused.", h.player.Pause(ctx, Target{}))
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
