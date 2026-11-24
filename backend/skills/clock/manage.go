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
//
// Among is set when Alfred asked "Which one?": the ids of the entries it
// listed, so the answer ("the 10 minute one", "all of them") picks among
// those instead of starting over. It is not a slot; recognizers never fill
// it.
type CancelTimer struct {
	What  string `slot:"what,optional"`
	Among []int64
}

func (CancelTimer) IntentName() string { return "timer_cancel" }

// ListTimers says what is set and how long is left: "how much time is
// left", "what alarms do I have".
type ListTimers struct {
	What string `slot:"what,optional"`
}

func (ListTimers) IntentName() string { return "timer_list" }

// Snooze is "snooze" (9 minutes) or "snooze for 5 minutes": a ringing
// timer, alarm or reminder goes off again later.
type Snooze struct {
	When string `slot:"when,optional"`
}

func (Snooze) IntentName() string { return "timer_snooze" }

// ExtendTimer is "add 5 minutes to the timer".
type ExtendTimer struct {
	Amount string `slot:"amount"`
	What   string `slot:"what,optional"`
}

func (ExtendTimer) IntentName() string { return "timer_extend" }

// snoozeFor is how long "snooze" waits, like most alarm clocks.
const snoozeFor = 9 * time.Minute

func registerManage(r *assistant.Router, h *handler) {
	assistant.Handle(r, assistant.Spec{
		Description: "Snooze a ringing alarm, timer or reminder",
		Phrases:     []string{"snooze", "snooze it", "snooze the alarm", "snooze for {when}", "snooze the alarm for {when}"},
	}, h.snooze)
	assistant.Handle(r, assistant.Spec{
		Description: "Add time to a timer",
		// The phrases end in "timer", so they win over the lists' "add
		// {items} to the {list}".
		Phrases: []string{
			"add {amount} to the timer", "add {amount} to my timer",
			"add {amount} to the {what} timer", "add {amount} to my {what} timer",
		},
	}, h.extendTimer)
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
	"them": true,
}

func parseSelection(what string) selection {
	var sel selection
	for _, w := range strings.Fields(match.Normalize(what)) {
		switch {
		case w == "all" || w == "every" || w == "everything" || w == "both":
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
	entries := h.timers.list()
	if listed := slices.DeleteFunc(slices.Clone(entries), func(e entry) bool { return !slices.Contains(i.Among, e.ID) }); len(listed) > 0 {
		entries = listed // the ones Alfred asked about, those still set
	}
	var all, matched []entry
	for _, e := range entries {
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
		ids := make([]int64, len(matched))
		for i, e := range matched {
			names[i] = e.name(now)
			ids[i] = e.ID
		}
		// The answer ("the 10 minute one", "all of them") is a new What,
		// among the ones just listed.
		return assistant.Response{
			Text: fmt.Sprintf("You have %d: %s. Which one?", len(matched), spoken.JoinAnd(names)),
			Ask:  &assistant.Question{Intent: CancelTimer{Among: ids}, Slot: "what"},
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

func (h *handler) snooze(ctx context.Context, i Snooze) (assistant.Response, error) {
	d := snoozeFor
	if i.When != "" {
		w, err := spoken.ParseWhen(i.When, h.now())
		if err != nil {
			return assistant.Response{}, err
		}
		d = w.At.Sub(h.now())
	}
	n, err := h.timers.snooze(ctx, d)
	if err != nil {
		return assistant.Response{}, err
	}
	if n == 0 {
		return assistant.Response{Text: "Nothing is ringing."}, nil
	}
	return assistant.Response{Text: "Snoozed for " + spoken.FormatDuration(d) + "."}, nil
}

func (h *handler) extendTimer(ctx context.Context, i ExtendTimer) (assistant.Response, error) {
	now := h.now()
	var amount []string
	for _, w := range strings.Fields(i.Amount) {
		if w != "more" && w != "another" && w != "extra" {
			amount = append(amount, w)
		}
	}
	w, err := spoken.ParseWhen(strings.Join(amount, " "), now)
	if err != nil || w.Duration <= 0 {
		return assistant.Response{}, &assistant.SpokenError{
			Text: "Sorry, how much time should I add?",
			Err:  errors.WithMessagef(fault.ErrInvalidValue, "amount %q", i.Amount),
		}
	}
	sel := parseSelection(i.What)
	sel.kinds = []Kind{KindTimer}
	var matched []entry
	for _, e := range h.timers.list() {
		if sel.matches(e, now) {
			matched = append(matched, e)
		}
	}
	switch {
	case len(matched) == 0:
		return assistant.Response{Text: "You have no timers."}, nil
	case len(matched) > 1:
		names := make([]string, len(matched))
		for i, e := range matched {
			names[i] = e.name(now)
		}
		return assistant.Response{
			Text: fmt.Sprintf("You have %d: %s. Which one?", len(matched), spoken.JoinAnd(names)),
			Ask:  &assistant.Question{Intent: ExtendTimer{Amount: i.Amount}, Slot: "what"},
		}, nil
	}
	e, err := h.timers.extend(ctx, matched[0].ID, w.Duration)
	if err != nil {
		return assistant.Response{}, err
	}
	return assistant.Response{Text: "Your timer now has " + spoken.FormatRemaining(e.At.Sub(now)) + " left."}, nil
}
