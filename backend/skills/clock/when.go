package clock

import (
	"strconv"
	"strings"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

// when is when a timer goes off.
type when struct {
	At time.Time
	// Duration is set for a duration ("in 10 minutes"); 0 for a clock time
	// ("at 7").
	Duration time.Duration
}

// maxDuration bounds timers; clock times are within two days anyway.
const maxDuration = 24 * time.Hour

// parseWhen parses a spoken time, normalized (match.Normalize turns "7:30
// a.m." into "7 30 a m"): a duration ("10 minutes", "an hour and a half")
// or a clock time ("7", "7 30 pm", "half past 7", "tomorrow at noon"). A
// clock time without am/pm is the next occurrence of either: at 22:00, "7"
// is 07:00 tomorrow; at 10:00 it is 19:00. Failures are SpokenErrors.
func parseWhen(text string, now time.Time) (when, error) {
	tokens := strings.Fields(text)
	for len(tokens) > 0 && leadingFiller[tokens[0]] {
		tokens = tokens[1:]
	}
	if d, ok := parseDuration(tokens); ok {
		if d > maxDuration {
			return when{}, &assistant.SpokenError{
				Text: "Sorry, I can only set timers for up to 24 hours.",
				Err:  errors.WithMessagef(fault.ErrInvalidValue, "duration %s over %s", d, maxDuration),
			}
		}
		return when{At: now.Add(d), Duration: d}, nil
	}
	if at, ok := parseClock(tokens, now); ok {
		return when{At: at}, nil
	}
	return when{}, &assistant.SpokenError{
		Text: "Sorry, I didn't get when.",
		Err:  errors.WithMessagef(fault.ErrInvalidValue, "no time in %q", text),
	}
}

var leadingFiller = map[string]bool{"in": true, "at": true, "for": true, "after": true}

var units = map[string]time.Duration{
	"second": time.Second, "seconds": time.Second, "sec": time.Second, "secs": time.Second,
	"minute": time.Minute, "minutes": time.Minute, "min": time.Minute, "mins": time.Minute,
	"hour": time.Hour, "hours": time.Hour, "hr": time.Hour, "hrs": time.Hour,
}

// parseDuration parses one or more "<n> <unit>" parts, e.g. "1 hour and 30
// minutes", "an hour and a half", "half an hour", "1 and a half hours".
func parseDuration(tokens []string) (time.Duration, bool) {
	var total time.Duration
	parts := 0
	for i := 0; i < len(tokens); {
		if tokens[i] == "and" && parts > 0 {
			i++
			continue
		}
		var n float64
		switch tokens[i] {
		case "half": // half an hour, half hour
			n, i = 0.5, skip(tokens, i+1, "a", "an")
		case "quarter": // quarter of an hour
			n, i = 0.25, skip(tokens, skip(tokens, i+1, "of"), "a", "an")
		case "a", "an":
			n, i = 1, i+1
		default:
			v, next, ok := number(tokens, i)
			if !ok {
				return 0, false
			}
			n, i = float64(v), next
			if andAHalf(tokens, i) && i+3 < len(tokens) && units[tokens[i+3]] != 0 {
				n, i = n+0.5, i+3
			}
		}
		if i >= len(tokens) || units[tokens[i]] == 0 {
			return 0, false
		}
		unit := units[tokens[i]]
		total += time.Duration(n * float64(unit))
		parts++
		i++
		if andAHalf(tokens, i) {
			total += unit / 2
			i += 3
		}
	}
	return total, parts > 0 && total > 0
}

// andAHalf reports whether tokens[i:] starts with "and a half".
func andAHalf(tokens []string, i int) bool {
	return i+3 <= len(tokens) && tokens[i] == "and" && tokens[i+1] == "a" && tokens[i+2] == "half"
}

// skip returns i advanced past tokens[i] if it is one of words.
func skip(tokens []string, i int, words ...string) int {
	if i < len(tokens) {
		for _, w := range words {
			if tokens[i] == w {
				return i + 1
			}
		}
	}
	return i
}

// parseClock parses a clock time with optional day and part of the day.
func parseClock(tokens []string, now time.Time) (time.Time, bool) {
	days := 0
	meridiem := "" // "am", "pm" or ""
	var rest []string
	for i := 0; i < len(tokens); i++ {
		switch t := tokens[i]; t {
		case "tomorrow":
			days = 1
		case "today", "at", "in", "the", "this":
		case "oclock":
		case "o": // "o'clock" normalizes to "o clock"; "seven o five" keeps it
			if i+1 < len(tokens) && tokens[i+1] == "clock" {
				i++
			} else {
				rest = append(rest, t)
			}
		case "am", "pm":
			meridiem = t
		case "a", "p": // "a.m." normalizes to "a m"
			if i+1 >= len(tokens) || tokens[i+1] != "m" {
				return time.Time{}, false
			}
			meridiem = t + "m"
			i++
		case "morning":
			meridiem = "am"
		case "afternoon", "evening", "tonight":
			meridiem = "pm"
		default:
			rest = append(rest, t)
		}
	}
	h, m, fixed, ok := clockTime(rest)
	if !ok || h > 23 || m > 59 {
		return time.Time{}, false
	}

	var hours []int
	switch {
	case fixed:
		hours = []int{h}
	case meridiem == "am":
		if h > 12 {
			return time.Time{}, false
		}
		hours = []int{h % 12}
	case meridiem == "pm":
		if h > 12 {
			hours = []int{h}
		} else {
			hours = []int{h%12 + 12}
		}
	case h == 0 || h > 12:
		hours = []int{h}
	default: // 12-hour clock without am/pm: whichever comes first
		hours = []int{h % 12, h%12 + 12}
	}

	var best time.Time
	day := now.AddDate(0, 0, days)
	for _, hour := range hours {
		t := time.Date(day.Year(), day.Month(), day.Day(), hour, m, 0, 0, now.Location())
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		if best.IsZero() || t.Before(best) {
			best = t
		}
	}
	return best, true
}

// clockTime parses the time itself: "7", "7 30", "730", "seven oh five",
// "half past 7", "quarter to 8", "10 past 7", "noon", "midnight". fixed is
// set when the hour is unambiguous (noon, midnight).
func clockTime(tokens []string) (h, m int, fixed, ok bool) {
	if len(tokens) == 1 {
		switch tokens[0] {
		case "noon", "midday":
			return 12, 0, true, true
		case "midnight":
			return 0, 0, true, true
		}
	}

	// half past 7, quarter to 8, 10 (minutes) past 7
	for k, t := range tokens {
		if t != "past" && t != "after" && t != "to" && t != "before" {
			continue
		}
		var minutes int
		switch before := tokens[:k]; {
		case len(before) == 1 && before[0] == "half" && (t == "past" || t == "after"):
			minutes = 30
		case len(before) == 1 && before[0] == "quarter":
			minutes = 15
		default:
			if n := len(before); n > 0 && (before[n-1] == "minutes" || before[n-1] == "minute") {
				before = before[:n-1]
			}
			v, next, ok := number(before, 0)
			if !ok || next != len(before) || v < 1 || v > 59 {
				return 0, 0, false, false
			}
			minutes = v
		}
		hour, next, ok := number(tokens, k+1)
		if !ok || next != len(tokens) || hour > 23 {
			return 0, 0, false, false
		}
		if t == "past" || t == "after" {
			return hour, minutes, false, true
		}
		hour--
		switch hour {
		case -1:
			hour = 23
		case 0:
			hour = 12 // "quarter to 1" may be 00:45 or 12:45
		}
		return hour, 60 - minutes, false, true
	}

	// 730, 1930
	if len(tokens) == 1 && len(tokens[0]) >= 3 && len(tokens[0]) <= 4 {
		if v, err := strconv.Atoi(tokens[0]); err == nil {
			return v / 100, v % 100, false, true
		}
	}

	// 7, 7 30, seven oh five
	h, i, ok := number(tokens, 0)
	if !ok {
		return 0, 0, false, false
	}
	if i < len(tokens) && (tokens[i] == "oh" || tokens[i] == "o") {
		i++
	}
	if i < len(tokens) {
		if m, i, ok = number(tokens, i); !ok {
			return 0, 0, false, false
		}
	}
	return h, m, false, i == len(tokens)
}

var ones = map[string]int{
	"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9,
	"ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15, "sixteen": 16,
	"seventeen": 17, "eighteen": 18, "nineteen": 19,
}

var tens = map[string]int{"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50}

// number parses a number at tokens[i]: digits ("25") or words ("twenty
// five"), returning it and the index after it.
func number(tokens []string, i int) (n, next int, ok bool) {
	if i >= len(tokens) {
		return 0, i, false
	}
	t := tokens[i]
	if v, err := strconv.Atoi(t); err == nil && v >= 0 {
		return v, i + 1, true
	}
	if v, ok := ones[t]; ok {
		return v, i + 1, true
	}
	if v, ok := tens[t]; ok {
		if i+1 < len(tokens) {
			if u, ok := ones[tokens[i+1]]; ok && u > 0 && u < 10 {
				return v + u, i + 2, true
			}
		}
		return v, i + 1, true
	}
	return 0, i, false
}
