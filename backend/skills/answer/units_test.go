package answer

import (
	"math"
	"testing"
)

func TestConvert(t *testing.T) {
	for _, tt := range []struct {
		v        float64
		from, to string
		want     float64
	}{
		{1, "lb", "g", 453.59237}, {3, "m", "ft", 9.84251968503937}, {30, "c", "f", 86}, {212, "f", "c", 100},
		{0, "c", "k", 273.15}, {100, "kmh", "mph", 62.13711922373339}, {1, "day", "s", 86400}, {1, "l", "cup", 4.226752837730889},
	} {
		got, err := convert(tt.v, tt.from, tt.to)
		if err != nil || math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("convert(%v %s → %s) = %v, %v; want %v", tt.v, tt.from, tt.to, got, err, tt.want)
		}
	}
	for _, pair := range [][2]string{{"kg", "m"}, {"lb", "parsec"}, {"c", "s"}} {
		if _, err := convert(1, pair[0], pair[1]); err == nil {
			t.Errorf("convert %s → %s succeeded", pair[0], pair[1])
		}
	}
	if got := unitName(1, "lb") + " is " + unitName(453.59237, "g"); got != "1 pound is 453.59 grams" {
		t.Errorf("unitName: %q", got)
	}
}

func TestUnitAfterValue(t *testing.T) {
	for q, want := range map[string]string{
		"what is 30 degrees celsius in fahrenheit": "c",
		"how many grams in a pound":                "lb",
		"how many feet is 3 meters":                "m",
		"convert 100 kilometers per hour to mph":   "km",
		"what is the weather":                      "",
	} {
		value := map[string]float64{"what is 30 degrees celsius in fahrenheit": 30, "how many feet is 3 meters": 3,
			"convert 100 kilometers per hour to mph": 100}[q]
		if value == 0 {
			value = 1
		}
		if got := unitAfterValue(q, value); got != want {
			t.Errorf("unitAfterValue(%q) = %q, want %q", q, got, want)
		}
	}
}
