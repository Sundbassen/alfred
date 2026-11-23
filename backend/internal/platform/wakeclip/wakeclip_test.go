package wakeclip

import (
	"testing"
	"time"
)

func TestNameParseRoundTrip(t *testing.T) {
	at := time.Date(2026, 11, 23, 18, 50, 33, 368_000_000, time.Local)
	name := Name(at, Near, 0.0123)
	if want := "20261123-185033.368_near_0.012.wav"; name != want {
		t.Fatalf("Name = %q, want %q", name, want)
	}
	clip, ok := Parse("some/dir/" + name)
	if !ok || clip.Kind != Near || !clip.HasScore || clip.Score != 0.012 {
		t.Errorf("Parse(%q) = %+v, %v", name, clip, ok)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		want Clip
		ok   bool
	}{
		{"20261123-185033.368_hit_0.244.wav", Clip{Kind: Hit, Score: 0.244, HasScore: true}, true},
		{"20260719-154130.874_miss.wav", Clip{Kind: Miss}, true},
		{"20260719-154312.353_hit.wav", Clip{Kind: Hit}, true},
		{"20261123-185033.368_error.wav", Clip{}, false},
		{"20261123-185033.368_hit_abc.wav", Clip{}, false},
		{"custom_0001.wav", Clip{}, false},
	}
	for _, tt := range tests {
		got, ok := Parse(tt.name)
		if ok != tt.ok || got != tt.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}
