// Package clock is the clock skill: it tells the time and sets timers,
// alarms and reminders, which are one intent: a point in time when Alfred
// chimes, plus an optional message to say.
package clock

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/spoken"
)

// AskTime is the intent "what time is it".
type AskTime struct{}

func (AskTime) IntentName() string { return "time" }

// AskDate is the intent "what's the date" / "what day is it".
type AskDate struct{}

func (AskDate) IntentName() string { return "date" }

// SetTimer sets a timer ("set a timer for 10 minutes"), an alarm ("wake me
// up at 7") or a reminder ("remind me to call mom in 10 minutes"). Which
// one follows from what was said: a message makes it a reminder, otherwise
// a duration is a timer and a clock time an alarm.
//
// The reminder phrases capture the time with the message ("remind me to
// {message}"), and the handler splits it off: templates such as "remind me
// to {message} in {when}" split "look in the oven at 7" at the wrong word.
type SetTimer struct {
	When    string `slot:"when,optional"`
	Message string `slot:"message,optional"`
}

func (SetTimer) IntentName() string { return "timer_set" }

// Kind is what the user set, derived from what they said.
type Kind int

const (
	// KindTimer is a duration without a message: "set a timer for 10 minutes".
	KindTimer Kind = iota
	// KindAlarm is a clock time without a message: "wake me up at 7".
	KindAlarm
	// KindReminder has a message: "remind me to call mom in 10 minutes".
	KindReminder
)

func (k Kind) String() string {
	switch k {
	case KindAlarm:
		return "alarm"
	case KindReminder:
		return "reminder"
	default:
		return "timer"
	}
}

// Timer is a timer, alarm or reminder as it is stored.
type Timer struct {
	ID   int64
	Kind Kind
	At   time.Time
	// Duration is what a timer was set for ("10 minutes"); 0 for a clock
	// time.
	Duration time.Duration
	// Message is a reminder's message, in the second person ("call your
	// mom"); empty otherwise.
	Message string
}

// Register adds the clock's intents to r; timers keeps the timers, alarms
// and reminders.
func Register(r *assistant.Router, timers *Timers) {
	register(r, timers, time.Now)
}

func register(r *assistant.Router, timers *Timers, now func() time.Time) {
	h := &handler{now: now, timers: timers}
	assistant.Handle(r, assistant.Spec{
		Description: "Tell the current local time",
		Phrases: []string{
			"what time is it",
			"what is the time",
			"what is the time now",
			"tell me the time",
			"current time",
		},
		Help: "tell you the time and the date",
	}, h.askTime)
	assistant.Handle(r, assistant.Spec{
		Description: "Tell today's date and day of the week",
		Phrases: []string{
			"what is the date",
			"what day is it",
			"what is today's date",
			"what is the date today",
			"what day is it today",
			"what day is today",
		},
	}, h.askDate)
	assistant.Handle(r, assistant.Spec{
		Description: "Set a timer, an alarm or a reminder: when it goes off, Alfred chimes and says the message, if any",
		Phrases: []string{
			"set a timer for {when}",
			"set a {when} timer",
			"start a timer for {when}",
			"timer for {when}",
			"set an alarm for {when}",
			"set an alarm at {when}",
			"wake me up at {when}",
			"wake me up in {when}",
			"remind me to {message}",
			"remind me {message}",
		},
		Help: "set timers, alarms and reminders",
	}, h.setTimer)
	registerManage(r, h)
}

type handler struct {
	now    func() time.Time
	timers *Timers
}

func (h *handler) askTime(ctx context.Context, _ AskTime) (assistant.Response, error) {
	return assistant.Response{Text: fmt.Sprintf("It's %s.", h.now().Format("15:04"))}, nil
}

func (h *handler) askDate(ctx context.Context, _ AskDate) (assistant.Response, error) {
	return assistant.Response{Text: "It's " + h.now().Format("Monday, January 2") + "."}, nil
}

func (h *handler) setTimer(ctx context.Context, i SetTimer) (assistant.Response, error) {
	now := h.now()
	whenText := match.Normalize(i.When)
	message := match.Normalize(i.Message)
	if t, rest, ok := splitReminder(message, now); ok {
		// The time came with the message ("call mom in 10 minutes"); a
		// recognizer that filled When too repeated it.
		if whenText == "" {
			whenText = t
		}
		message = rest
	}
	message = strings.TrimPrefix(message, "to ")
	if whenText == "" {
		return assistant.Response{}, &assistant.SpokenError{
			Text: "Sorry, I didn't get when.",
			Err:  errors.WithMessagef(fault.ErrInvalidValue, "no time in %q", i.Message),
		}
	}
	w, err := spoken.ParseWhen(whenText, now)
	if err != nil {
		return assistant.Response{}, err
	}
	if w.Duration > maxDuration {
		return assistant.Response{}, &assistant.SpokenError{
			Text: "Sorry, I can only set timers for up to 24 hours.",
			Err:  errors.WithMessagef(fault.ErrInvalidValue, "duration %s over %s", w.Duration, maxDuration),
		}
	}

	kind := KindAlarm
	switch {
	case message != "":
		kind = KindReminder
	case w.Duration > 0:
		kind = KindTimer
	}
	e, err := h.timers.add(ctx, Timer{Kind: kind, At: w.At, Duration: w.Duration, Message: spoken.SecondPerson(message)})
	if err != nil {
		return assistant.Response{}, err
	}

	var text string
	switch e.Kind {
	case KindTimer:
		text = "Timer set for " + spoken.FormatDuration(e.Duration) + "."
	case KindAlarm:
		text = "Alarm set for " + spoken.FormatClock(e.At, now) + "."
	case KindReminder:
		at := "at " + spoken.FormatClock(e.At, now)
		if e.Duration > 0 {
			at = "in " + spoken.FormatDuration(e.Duration)
		}
		text = "OK, I'll remind you to " + e.Message + " " + at + "."
	}
	return assistant.Response{Text: text}, nil
}

// maxDuration bounds timers; clock times are within two days anyway.
const maxDuration = 24 * time.Hour

// timeMarkers start a time in a reminder: "in 10 minutes", "at 7",
// "tomorrow at 7", "tonight at 8".
var timeMarkers = map[string]bool{"in": true, "at": true, "tomorrow": true, "tonight": true}

// splitReminder splits a reminder into its time and message, with the time
// first ("in 10 minutes to call mom") or last ("call mom in 10 minutes"). It
// takes the shortest leading time followed by "to", or else the longest
// trailing time, so "look at the oven in 10 minutes" splits at "in".
func splitReminder(text string, now time.Time) (whenText, message string, ok bool) {
	tokens := strings.Fields(text)
	parses := func(ts []string) bool {
		_, err := spoken.ParseWhen(strings.Join(ts, " "), now)
		return err == nil
	}
	if len(tokens) > 0 && timeMarkers[tokens[0]] {
		for k := 1; k < len(tokens)-1; k++ {
			if tokens[k] == "to" && parses(tokens[:k]) {
				return strings.Join(tokens[:k], " "), strings.Join(tokens[k+1:], " "), true
			}
		}
	}
	for i := 1; i < len(tokens); i++ {
		if timeMarkers[tokens[i]] && parses(tokens[i:]) {
			return strings.Join(tokens[i:], " "), strings.Join(tokens[:i], " "), true
		}
	}
	return "", text, false
}
