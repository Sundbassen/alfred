package spoken

import (
	"testing"
	"time"
)

func TestParseDay(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.Local) }
	// at10 is Monday 23 November.
	tests := map[string]time.Time{
		"":                       day(23),
		"today":                  day(23),
		"tonight":                day(23),
		"tomorrow":               day(24),
		"the day after tomorrow": day(25),
		"friday":                 day(27),
		"on friday":              day(27),
		"this friday":            day(27),
		"monday":                 day(23),
		"next monday":            day(30),
		"sunday":                 day(29),
		"this weekend":           day(28),
	}
	for text, want := range tests {
		if got, ok := ParseDay(text, at10); !ok || !got.Equal(want) {
			t.Errorf("ParseDay(%q) = %s, %v; want %s", text, got.Format("Mon Jan 2"), ok, want.Format("Mon Jan 2"))
		}
	}
	for _, text := range []string{"banana", "friday morning", "next tomorrow", "last friday"} {
		if got, ok := ParseDay(text, at10); ok {
			t.Errorf("ParseDay(%q) = %s, want no day", text, got)
		}
	}
}
