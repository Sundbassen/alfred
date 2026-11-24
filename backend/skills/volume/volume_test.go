package volume

import (
	"context"
	"math"
	"testing"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
)

type fakeMixer struct {
	level float64
	muted bool
}

func (m *fakeMixer) Volume(context.Context) (float64, bool, error) { return m.level, m.muted, nil }
func (m *fakeMixer) SetVolume(_ context.Context, l float64) error  { m.level = l; return nil }
func (m *fakeMixer) SetMute(_ context.Context, muted bool) error   { m.muted = muted; return nil }

func say(t *testing.T, r *assistant.Router, transcript string) string {
	t.Helper()
	rec, err := match.New(r.Intents()).Recognize(context.Background(), transcript)
	if err != nil {
		t.Fatalf("%q: %v", transcript, err)
	}
	intent, _ := r.New(rec.Intent)
	intent, err = assistant.Fill(intent, rec.Slots)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := r.Dispatch(context.Background(), intent)
	if err != nil {
		return assistant.SpokenText(err)
	}
	return resp.Text
}

func TestVolume(t *testing.T) {
	m := &fakeMixer{level: 0.5}
	r := assistant.NewRouter()
	Register(r, m)
	steps := []struct {
		say, want string
		level     float64
		muted     bool
	}{
		{"Volume up.", "Volume 6.", 0.6, false},
		{"Turn it down.", "Volume 5.", 0.5, false},
		{"Set the volume to 3.", "Volume 3.", 0.3, false},
		{"Set the volume to eight.", "Volume 8.", 0.8, false},
		{"Volume 40 percent.", "Volume 4.", 0.4, false},
		{"Set the volume to max.", "Volume 10.", 1, false},
		{"Louder.", "Volume 10.", 1, false}, // capped
		{"Mute.", "", 1, true},
		{"Volume down.", "Volume 9.", 0.9, false}, // changing the level unmutes
		{"Mute.", "", 0.9, true},
		{"Unmute.", "Volume 9.", 0.9, false},
		{"Set the volume to banana.", "Sorry, say a volume from 0 to 10.", 0.9, false},
	}
	for _, s := range steps {
		if got := say(t, r, s.say); got != s.want || math.Abs(m.level-s.level) > 1e-9 || m.muted != s.muted {
			t.Errorf("%q: %q, level %.2f muted %v; want %q, %.2f, %v", s.say, got, m.level, m.muted, s.want, s.level, s.muted)
		}
	}
}
