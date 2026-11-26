package volume

import (
	"context"
	"math"
	"testing"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
)

type fakeMixer struct {
	level float64
	muted bool
	err   error // returned by every call when set
}

func (m *fakeMixer) Volume(context.Context) (float64, bool, error) { return m.level, m.muted, m.err }

func (m *fakeMixer) SetVolume(_ context.Context, l float64) error {
	if m.err == nil {
		m.level = l
	}
	return m.err
}

func (m *fakeMixer) SetMute(_ context.Context, muted bool) error {
	if m.err == nil {
		m.muted = muted
	}
	return m.err
}

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
		{"Turn the volume up to 8.", "Volume 8.", 0.8, false},
		{"Turn it down to 3.", "Volume 3.", 0.3, false},
		{"Set the volume to 5%.", "Volume 1.", 0.05, false},
	}
	for _, s := range steps {
		if got := say(t, r, s.say); got != s.want || math.Abs(m.level-s.level) > 1e-9 || m.muted != s.muted {
			t.Errorf("%q: %q, level %.2f muted %v; want %q, %.2f, %v", s.say, got, m.level, m.muted, s.want, s.level, s.muted)
		}
	}
}

func TestLouderAbove100Percent(t *testing.T) {
	// Another program set the sink to 150 %: louder must not lower it.
	m := &fakeMixer{level: 1.5}
	r := assistant.NewRouter()
	Register(r, m)
	if got := say(t, r, "Louder."); got != "Volume 10." || m.level != 1 {
		t.Errorf("louder from 150 %%: %q, level %.2f; want Volume 10., 1.00", got, m.level)
	}
}

func TestMixerErrors(t *testing.T) {
	broken := errors.New("wpctl failed")
	for _, transcript := range []string{"Volume up.", "Set the volume to 3.", "Mute.", "Unmute."} {
		r := assistant.NewRouter()
		Register(r, &fakeMixer{level: 0.5, err: broken})
		rec, err := match.New(r.Intents()).Recognize(context.Background(), transcript)
		if err != nil {
			t.Fatal(err)
		}
		intent, _ := r.New(rec.Intent)
		if intent, err = assistant.Fill(intent, rec.Slots); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Dispatch(context.Background(), intent); !errors.Is(err, broken) {
			t.Errorf("%q: %v, want the mixer's error", transcript, err)
		}
	}
}

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		text string
		want float64
		ok   bool
	}{
		{"5", 0.5, true},
		{"five", 0.5, true},
		{"0", 0, true},
		{"zero", 0, true},
		{"10", 1, true},
		{"40", 0.4, true},
		{"5 percent", 0.05, true},
		{"100 percent", 1, true},
		{"half", 0.5, true},
		{"full", 1, true},
		{"min", 0, true},
		{"101", 0, false},
		{"5 please now", 0, false},
		{"banana", 0, false},
	} {
		got, ok := parseLevel(tc.text)
		if ok != tc.ok || math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("parseLevel(%q) = %.2f, %v; want %.2f, %v", tc.text, got, ok, tc.want, tc.ok)
		}
	}
}
