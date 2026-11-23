package music

import (
	"context"
	"strings"
	"testing"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

// fakePlayer records calls and returns scripted results.
type fakePlayer struct {
	calls   []string
	item    Item
	artists []string
	err     error
}

func (f *fakePlayer) Play(_ context.Context, _ Target, query string) (Item, error) {
	f.calls = append(f.calls, "play:"+query)
	return f.item, f.err
}

func (f *fakePlayer) PlayMix(_ context.Context, _ Target, artists []string) ([]string, error) {
	f.calls = append(f.calls, "mix:"+strings.Join(artists, "|"))
	return f.artists, f.err
}

func (f *fakePlayer) Pause(context.Context, Target) error    { return f.record("pause") }
func (f *fakePlayer) Resume(context.Context, Target) error   { return f.record("resume") }
func (f *fakePlayer) Next(context.Context, Target) error     { return f.record("next") }
func (f *fakePlayer) Previous(context.Context, Target) error { return f.record("previous") }

func (f *fakePlayer) record(call string) error {
	f.calls = append(f.calls, call)
	return f.err
}

// respond runs a transcript through the real recognizer and router.
func respond(t *testing.T, p *fakePlayer, transcript string) (*app.Reply, error) {
	t.Helper()
	r := assistant.NewRouter()
	Register(r, p)
	return app.NewAssistant(r).Respond(context.Background(), transcript)
}

func TestTranscriptsReachThePlayer(t *testing.T) {
	tests := []struct {
		transcript string
		player     fakePlayer
		wantCall   string
		wantText   string
	}{
		{"Hey Alfred, play Daft Punk.", fakePlayer{item: Item{Title: "Get Lucky", Artist: "Daft Punk"}},
			"play:daft punk", "Playing Get Lucky by Daft Punk."},
		{"play some jazz", fakePlayer{item: Item{Title: "Jazz Classics"}}, "play:jazz", "Playing Jazz Classics."},
		{"Play a radio with Daft Punk and Justice.", fakePlayer{artists: []string{"Daft Punk", "Justice"}},
			"mix:daft punk|justice", "Playing a mix of Daft Punk and Justice."},
		{"play a mix of air", fakePlayer{artists: []string{"Air"}}, "mix:air", "Playing a mix of Air."},
		{"pause the music", fakePlayer{}, "pause", "Paused."},
		{"play", fakePlayer{}, "resume", "Resuming."},
		{"next song", fakePlayer{}, "next", "Skipping."},
		{"go back", fakePlayer{}, "previous", "Going back."},
	}
	for _, tt := range tests {
		p := tt.player
		reply, err := respond(t, &p, tt.transcript)
		if err != nil {
			t.Errorf("%q: %v", tt.transcript, err)
			continue
		}
		if len(p.calls) != 1 || p.calls[0] != tt.wantCall || reply.Text != tt.wantText {
			t.Errorf("%q: calls %v, reply %q; want [%s], %q", tt.transcript, p.calls, reply.Text, tt.wantCall, tt.wantText)
		}
	}
}

func TestNotFoundIsSpoken(t *testing.T) {
	p := &fakePlayer{err: errors.WithMessage(fault.ErrNotFound, "no search results")}
	_, err := respond(t, p, "play zzyzx")
	if got, want := assistant.SpokenText(err), "I couldn't find anything called zzyzx."; got != want {
		t.Errorf("SpokenText = %q, want %q", got, want)
	}

	_, err = respond(t, p, "play a radio with daft punk and nobody")
	if got, want := assistant.SpokenText(err), "I couldn't find all of daft punk and nobody."; got != want {
		t.Errorf("SpokenText = %q, want %q", got, want)
	}
}

func TestProviderErrorsKeepTheirCategory(t *testing.T) {
	p := &fakePlayer{err: errors.WithMessage(fault.ErrUnauthorized, "token revoked")}
	_, err := respond(t, p, "pause")
	if !errors.Is(err, fault.ErrUnauthorized) {
		t.Errorf("error = %v, want fault.ErrUnauthorized", err)
	}
	if got, want := assistant.SpokenText(err), "I need you to log in to that service again."; got != want {
		t.Errorf("SpokenText = %q, want %q", got, want)
	}
}

func TestMixArtistCount(t *testing.T) {
	p := &fakePlayer{}
	_, err := respond(t, p, "play a mix of a and b and c and d and e and f")
	if !errors.Is(err, fault.ErrInvalidValue) || len(p.calls) != 0 {
		t.Errorf("six artists: error %v, calls %v; want ErrInvalidValue and no call", err, p.calls)
	}
}

func TestJoinNames(t *testing.T) {
	for in, want := range map[string]string{"": "", "a": "a", "a|b": "a and b", "a|b|c": "a, b and c"} {
		var names []string
		if in != "" {
			names = strings.Split(in, "|")
		}
		if got := joinNames(names); got != want {
			t.Errorf("joinNames(%v) = %q, want %q", names, got, want)
		}
	}
}
