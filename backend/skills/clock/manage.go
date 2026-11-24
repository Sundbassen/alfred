package clock

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/spoken"
)

// CancelTimer cancels a timer, alarm or reminder: "cancel the timer",
// "cancel my 7 o'clock alarm", "cancel the reminder to call mom", "cancel
// all timers". With one pending, "cancel it" is enough.
type CancelTimer struct {
	What string `slot:"what,optional"`
}

func (CancelTimer) IntentName() string { return "timer_cancel" }

// ListTimers says what is set and how long is left: "how much time is
// left", "what alarms do I have".
type ListTimers struct {
	What string `slot:"what,optional"`
}

func (ListTimers) IntentName() string { return "timer_list" }

func registerManage(r *assistant.Router, h *handler) {
	assistant.Handle(r, assistant.Spec{
		Description: "Cancel a timer, alarm or reminder, or all of them",
		Phrases: []string{
			"cancel {what}",
			"cancel the {what}",
			"cancel my {what}",
			"delete the {what}",
			"cancel it",
		},
	}, h.cancelTimer)
	assistant.Handle(r, assistant.Spec{
		Description: "Say which timers, alarms and reminders are set and how much time is left",
		Phrases: []string{
			"how much time is left",
			"how much time is left on the {what}",
			"how long is left",
			"how long is left on the {what}",
			"what {what} do i have",
			"do i have any {what}",
		},
	}, h.listTimers)
}

// selection is what a CancelTimer or ListTimers refers to.
type selection struct {
	all   bool   // "all", "every", "everything"
	kinds []Kind // empty: any kind
	words []string
}

// kindWords name a kind, singular or plural.
var kindWords = map[string]Kind{
	"timer": KindTimer, "timers": KindTimer,
	"alarm": KindAlarm, "alarms": KindAlarm,
	"reminder": KindReminder, "reminders": KindReminder,
}

// selectionFiller carries no meaning in a selection.
var selectionFiller = map[string]bool{
	"the": true, "my": true, "a": true, "an": true, "it": true, "that": true, "this": true, "one": true,
	"for": true, "to": true, "about": true, "set": true, "of": true, "and": true, "any": true, "on": true,
	"them": true, "both": true,
}

func parseSelection(what string) selection {
	var sel selection
	for _, w := range strings.Fields(match.Normalize(what)) {
		switch {
		case w == "all" || w == "every" || w == "everything":
			sel.all = true
		case selectionFiller[w]:
		default:
			if k, ok := kindWords[w]; ok {
				if !slices.Contains(sel.kinds, k) {
					sel.kinds = append(sel.kinds, k)
				}
				continue
			}
			sel.words = append(sel.words, w)
		}
	}
	return sel
}

// matches reports whether e fits the selection: its kind, and the
// remaining words as a time ("7 o'clock alarm", "10 minute timer") or as
// words of a reminder's message ("call mom").
func (sel selection) matches(e entry, now time.Time) bool {
	if len(sel.kinds) > 0 && !slices.Contains(sel.kinds, e.Kind) {
		return false
	}
	if len(sel.words) == 0 {
		return true
	}
	text := strings.Join(sel.words, " ")
	if w, err := spoken.ParseWhen(text, now); err == nil {
		if w.Duration > 0 {
			return e.Duration == w.Duration
		}
		// "7" may mean 07:00 or 19:00.
		return e.At.Minute() == w.At.Minute() && e.At.Hour()%12 == w.At.Hour()%12
	}
	message := " " + e.Message + " "
	for _, w := range strings.Fields(spoken.SecondPerson(text)) {
		if !strings.Contains(message, " "+w+" ") {
			return false
		}
	}
	return true
}

// noneText says that nothing of the selected kinds is set.
func (sel selection) noneText() string {
	if len(sel.kinds) == 0 {
		return "You have no timers, alarms or reminders."
	}
	names := make([]string, len(sel.kinds))
	for i, k := range sel.kinds {
		names[i] = k.String() + "s"
	}
	return "You have no " + spoken.JoinOr(names) + "."
}

func (h *handler) cancelTimer(ctx context.Context, i CancelTimer) (assistant.Response, error) {
	now := h.now()
	sel := parseSelection(i.What)
	var all, matched []entry
	for _, e := range h.timers.list() {
		if len(sel.kinds) == 0 || slices.Contains(sel.kinds, e.Kind) {
			all = append(all, e)
		}
		if sel.matches(e, now) {
			matched = append(matched, e)
		}
	}
	switch {
	case len(all) == 0:
		return assistant.Response{Text: sel.noneText()}, nil
	case len(matched) == 0:
		return assistant.Response{}, &assistant.SpokenError{
			Text: "Sorry, I couldn't find that one. " + listText(all, now),
			Err:  errors.WithMessagef(fault.ErrNotFound, "no timer matches %q", i.What),
		}
	case len(matched) > 1 && !sel.all:
		names := make([]string, len(matched))
		for i, e := range matched {
			names[i] = e.name(now)
		}
		// The answer ("the 10 minute one", "all of them") is a new What.
		return assistant.Response{
			Text: fmt.Sprintf("You have %d: %s. Which one?", len(matched), spoken.JoinAnd(names)),
			Ask:  &assistant.Question{Intent: CancelTimer{}, Slot: "what"},
		}, nil
	}

	ids := make([]int64, len(matched))
	for i, e := range matched {
		ids[i] = e.ID
	}
	h.timers.cancel(ids...)
	if len(matched) == 1 {
		return assistant.Response{Text: "Cancelled " + matched[0].name(now) + "."}, nil
	}
	return assistant.Response{Text: fmt.Sprintf("Cancelled all %d.", len(matched))}, nil
}

func (h *handler) listTimers(ctx context.Context, i ListTimers) (assistant.Response, error) {
	now := h.now()
	sel := parseSelection(i.What)
	var matched []entry
	for _, e := range h.timers.list() {
		if sel.matches(e, now) {
			matched = append(matched, e)
		}
	}
	if len(matched) == 0 {
		return assistant.Response{Text: sel.noneText()}, nil
	}
	return assistant.Response{Text: listText(matched, now)}, nil
}

// listText says each entry and when it goes off, soonest first.
func listText(entries []entry, now time.Time) string {
	sentences := make([]string, len(entries))
	for i, e := range entries {
		sentences[i] = e.status(now)
	}
	return strings.Join(sentences, " ")
}

// name names an entry: "your timer for 10 minutes", "your alarm for
// 07:00", "your reminder to call your mom".
func (e entry) name(now time.Time) string {
	switch e.Kind {
	case KindTimer:
		return "your timer for " + spoken.FormatDuration(e.Duration)
	case KindAlarm:
		return "your alarm for " + spoken.FormatClock(e.At, now)
	default:
		return "your reminder to " + e.Message
	}
}

// status says an entry and when it goes off: "Your timer for 10 minutes
// has 4 minutes left."
func (e entry) status(now time.Time) string {
	name := e.name(now)
	name = strings.ToUpper(name[:1]) + name[1:]
	switch {
	case e.ringing():
		return name + " is going off."
	case e.Kind == KindTimer:
		return name + " has " + spoken.FormatRemaining(e.At.Sub(now)) + " left."
	case e.Kind == KindAlarm:
		return "Your alarm is set for " + spoken.FormatClock(e.At, now) + "."
	case e.Duration > 0:
		return name + " is in " + spoken.FormatRemaining(e.At.Sub(now)) + "."
	default:
		return name + " is at " + spoken.FormatClock(e.At, now) + "."
	}
}
