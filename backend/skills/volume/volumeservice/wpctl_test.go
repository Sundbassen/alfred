package volumeservice

import (
	"context"
	"strings"
	"testing"
)

func TestParseVolume(t *testing.T) {
	for out, want := range map[string]struct {
		level float64
		muted bool
	}{
		"Volume: 0.40\n":         {0.4, false},
		"Volume: 1.00 [MUTED]\n": {1, true},
	} {
		level, muted, err := parseVolume(out)
		if err != nil || level != want.level || muted != want.muted {
			t.Errorf("parseVolume(%q) = %v, %v, %v", out, level, muted, err)
		}
	}
	if _, _, err := parseVolume("nope"); err == nil {
		t.Error("parseVolume(nope) succeeded")
	}
}

func TestCommands(t *testing.T) {
	var calls []string
	w := &Wpctl{run: func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return []byte("Volume: 0.50\n"), nil
	}}
	ctx := context.Background()
	w.SetVolume(ctx, 0.3)
	w.SetMute(ctx, true)
	w.Volume(ctx)
	want := []string{
		"set-volume --limit 1.0 @DEFAULT_AUDIO_SINK@ 0.30",
		"set-mute @DEFAULT_AUDIO_SINK@ 1",
		"get-volume @DEFAULT_AUDIO_SINK@",
	}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %q, want %q", calls, want)
	}
}
