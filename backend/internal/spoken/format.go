package spoken

import (
	"fmt"
	"strings"
	"time"
)

// FormatDuration says a duration: "10 minutes", "1 hour and 30 minutes".
func FormatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	var parts []string
	for _, u := range []struct {
		unit time.Duration
		name string
	}{{time.Hour, "hour"}, {time.Minute, "minute"}, {time.Second, "second"}} {
		if n := int(d / u.unit); n > 0 {
			parts = append(parts, plural(n, u.name))
			d -= time.Duration(n) * u.unit
		}
	}
	if len(parts) == 0 {
		return "0 seconds"
	}
	return JoinAnd(parts)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// JoinAnd joins "a", "b" and "c" as "a, b and c".
func JoinAnd(parts []string) string {
	if len(parts) <= 1 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// FormatClock says a clock time relative to now: "19:00", "07:00 tomorrow".
// at must be today or tomorrow; any other day is also said as "tomorrow".
func FormatClock(at, now time.Time) string {
	s := at.Format("15:04")
	ay, am, ad := at.Date()
	ny, nm, nd := now.Date()
	if ay != ny || am != nm || ad != nd {
		s += " tomorrow"
	}
	return s
}

// SecondPerson turns the user's words into Alfred's: "call my mom" becomes
// "call your mom".
func SecondPerson(text string) string {
	words := strings.Fields(text)
	for i, w := range words {
		switch w {
		case "my":
			words[i] = "your"
		case "me":
			words[i] = "you"
		case "myself":
			words[i] = "yourself"
		case "mine":
			words[i] = "yours"
		case "i":
			words[i] = "you"
			if i+1 < len(words) && words[i+1] == "am" {
				words[i+1] = "are"
			}
		}
	}
	return strings.Join(words, " ")
}

// JoinOr joins "a", "b" and "c" as "a, b or c".
func JoinOr(parts []string) string {
	if len(parts) <= 1 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
}

// FormatRemaining says time left, to the minute from 2 minutes up.
func FormatRemaining(d time.Duration) string {
	if d >= 2*time.Minute {
		d = d.Round(time.Minute)
	}
	if d < time.Second {
		return "less than a second"
	}
	return FormatDuration(d)
}
