package spoken

import (
	"strings"
	"time"
)

// ParseDay parses a day in the coming week, normalized: "" or "today",
// "tonight", "tomorrow", "the day after tomorrow", a weekday ("friday", "on
// friday", "this friday"; "next friday" skips this week's), "this weekend"
// (Saturday). It returns midnight of that day in now's location; a weekday
// that is today means today.
func ParseDay(text string, now time.Time) (time.Time, bool) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	next := false
	var words []string
	for _, w := range strings.Fields(text) {
		switch w {
		case "on", "this", "the", "for", "in":
		case "next":
			next = true
		default:
			words = append(words, w)
		}
	}
	switch strings.Join(words, " ") {
	case "", "today", "tonight", "now":
		return today, !next
	case "tomorrow":
		return today.AddDate(0, 0, 1), !next
	case "day after tomorrow":
		return today.AddDate(0, 0, 2), !next
	case "weekend":
		words = []string{"saturday"}
	}
	if len(words) != 1 {
		return time.Time{}, false
	}
	day, ok := weekdays[words[0]]
	if !ok {
		return time.Time{}, false
	}
	ahead := (int(day) - int(today.Weekday()) + 7) % 7
	if next && ahead == 0 {
		ahead = 7
	}
	return today.AddDate(0, 0, ahead), true
}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
}
