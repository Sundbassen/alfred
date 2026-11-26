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
		"next weekend":           day(28),
		"next friday":            day(27),
		"now":                    day(23),
	}
	for text, want := range tests {
		if got, ok := ParseDay(text, at10); !ok || !got.Equal(want) {
			t.Errorf("ParseDay(%q) = %s, %v; want %s", text, got.Format("Mon Jan 2"), ok, want.Format("Mon Jan 2"))
		}
	}
	// On a Sunday the weekend is today; on a Friday "next friday" is a week
	// ahead.
	sunday := at10.AddDate(0, 0, 6)
	if got, ok := ParseDay("this weekend", sunday); !ok || got.Day() != 29 {
		t.Errorf("ParseDay(this weekend) on Sunday = %s, %v; want Sun Nov 29", got.Format("Mon Jan 2"), ok)
	}
	if got, ok := ParseDay("next weekend", sunday); !ok || got.Day() != 5 {
		t.Errorf("ParseDay(next weekend) on Sunday = %s, %v; want Sat Dec 5", got.Format("Mon Jan 2"), ok)
	}
	if got, ok := ParseDay("next friday", at10.AddDate(0, 0, 4)); !ok || got.Day() != 4 {
		t.Errorf("ParseDay(next friday) on Friday = %s, %v; want Fri Dec 4", got.Format("Mon Jan 2"), ok)
	}
	for _, text := range []string{"banana", "friday morning", "next tomorrow", "last friday"} {
		if got, ok := ParseDay(text, at10); ok {
			t.Errorf("ParseDay(%q) = %s, want no day", text, got)
		}
	}
}
