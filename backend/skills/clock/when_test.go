package clock

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := map[string]time.Duration{
		"10 minutes":            10 * time.Minute,
		"for 10 minutes":        10 * time.Minute,
		"10 minute":             10 * time.Minute,
		"a minute":              time.Minute,
		"an hour and a half":    90 * time.Minute,
		"1 and a half hours":    90 * time.Minute,
		"half an hour":          30 * time.Minute,
		"quarter of an hour":    15 * time.Minute,
		"1 hour 30 minutes":     90 * time.Minute,
		"1 hour and 30 minutes": 90 * time.Minute,
		"90 seconds":            90 * time.Second,
		"twenty five minutes":   25 * time.Minute,
		"in five mins":          5 * time.Minute,
		"2 hrs":                 2 * time.Hour,
	}
	for text, want := range tests {
		w, err := parseWhen(text, at10)
		if err != nil || w.Duration != want || !w.At.Equal(at10.Add(want)) {
			t.Errorf("%q: %+v, %v; want %s", text, w, err, want)
		}
	}
}

func TestParseClock(t *testing.T) {
	day := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, time.Local) }
	at22 := day(23, 22, 0)
	tests := []struct {
		text string
		now  time.Time
		want time.Time
	}{
		{"7", at10, day(23, 19, 0)},
		{"7", at22, day(24, 7, 0)},
		{"at 7", day(23, 6, 0), day(23, 7, 0)},
		{"7 30 a m", at10, day(24, 7, 30)},
		{"7 30 am", at10, day(24, 7, 30)},
		{"7 pm", at10, day(23, 19, 0)},
		{"7 o clock", at10, day(23, 19, 0)},
		{"19 00", at10, day(23, 19, 0)},
		{"1930", at10, day(23, 19, 30)},
		{"seven thirty", at10, day(23, 19, 30)},
		{"seven o five", at10, day(23, 19, 5)},
		{"half past 7", at10, day(23, 19, 30)},
		{"quarter past 7", at10, day(23, 19, 15)},
		{"quarter to 8", at10, day(23, 19, 45)},
		{"10 past 11", at10, day(23, 11, 10)},
		{"20 minutes to 11", at10, day(23, 10, 40)},
		{"quarter to 1", at10, day(23, 12, 45)},
		{"noon", at10, day(23, 12, 0)},
		{"midnight", at10, day(24, 0, 0)},
		{"tomorrow at 7", at10, day(24, 7, 0)},
		{"7 tomorrow", at10, day(24, 7, 0)},
		{"tomorrow at 7 pm", at10, day(24, 19, 0)},
		{"8 in the morning", at10, day(24, 8, 0)},
		{"8 in the evening", at10, day(23, 20, 0)},
		{"12", at10, day(23, 12, 0)},
		{"10", at10, day(23, 22, 0)}, // 10:00 is now, so the next one
	}
	for _, tt := range tests {
		w, err := parseWhen(tt.text, tt.now)
		if err != nil || !w.At.Equal(tt.want) || w.Duration != 0 {
			t.Errorf("%q at %s: %+v, %v; want %s", tt.text, tt.now.Format("15:04"), w, err, tt.want.Format("Jan 2 15:04"))
		}
	}
}

func TestParseWhenFailures(t *testing.T) {
	for _, text := range []string{"", "banana", "0 minutes", "minutes", "25 00", "7 61", "13 am", "the oven", "in the morning"} {
		if w, err := parseWhen(text, at10); err == nil {
			t.Errorf("%q: %+v, want an error", text, w)
		}
	}
}

func TestFormat(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Minute:                      "10 minutes",
		time.Hour + 30*time.Minute:            "1 hour and 30 minutes",
		time.Hour + time.Minute + time.Second: "1 hour, 1 minute and 1 second",
		90 * time.Second:                      "1 minute and 30 seconds",
	} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%s) = %q, want %q", d, got, want)
		}
	}
	if got := secondPerson("tell me i am late for my meeting"); got != "tell you you are late for your meeting" {
		t.Errorf("secondPerson = %q", got)
	}
}
