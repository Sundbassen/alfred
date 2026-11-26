package clock

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sundbassen/alfred/internal/assistant"
)

// say dispatches a transcript and returns what Alfred answers (the reply
// or the spoken error).
func say(t *testing.T, r *assistant.Router, transcript string) string {
	t.Helper()
	resp, err := r.Dispatch(context.Background(), recognize(t, r, transcript))
	if err != nil {
		return assistant.SpokenText(err)
	}
	return resp.Text
}

// newManaged registers the clock in a synctest bubble, with the fake clock.
func newManaged(t *testing.T) (*assistant.Router, *Timers, *fakeAnnouncer) {
	out := &fakeAnnouncer{}
	timers := NewTimers(out, newFakeStore(), nil)
	t.Cleanup(timers.Close)
	r := assistant.NewRouter()
	register(r, timers, time.Now)
	return r, timers, out
}

func TestListTimers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _, _ := newManaged(t)
		if got := say(t, r, "How much time is left?"); got != "You have no timers, alarms or reminders." {
			t.Errorf("none: %q", got)
		}
		say(t, r, "Set a timer for 10 minutes.")
		say(t, r, "Remind me to call my mom in 1 hour.")
		say(t, r, "Set a timer for 2 minutes.")
		time.Sleep(30 * time.Second)

		want := "Your timer for 2 minutes has 1 minute and 30 seconds left. " +
			"Your timer for 10 minutes has 10 minutes left. " +
			"Your reminder to call your mom is in 1 hour."
		if got := say(t, r, "How much time is left?"); got != want {
			t.Errorf("all:\n got %q\nwant %q", got, want)
		}
		if got := say(t, r, "What reminders do I have?"); got != "Your reminder to call your mom is in 1 hour." {
			t.Errorf("reminders: %q", got)
		}
		if got := say(t, r, "Do I have any alarms?"); got != "You have no alarms." {
			t.Errorf("alarms: %q", got)
		}

		time.Sleep(90 * time.Second) // the 2 minute timer goes off
		synctest.Wait()
		if got := say(t, r, "How long is left on the 2 minute timer?"); got != "Your timer for 2 minutes is going off." {
			t.Errorf("ringing: %q", got)
		}
	})
}

func TestCancelTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, timers, _ := newManaged(t)
		if got := say(t, r, "Cancel the timer."); got != "You have no timers." {
			t.Errorf("none: %q", got)
		}

		say(t, r, "Set a timer for 10 minutes.")
		if got := say(t, r, "Cancel it."); got != "Cancelled your timer for 10 minutes." {
			t.Errorf("only one: %q", got)
		}

		say(t, r, "Set a timer for 10 minutes.")
		say(t, r, "Set a timer for 1 hour.")
		say(t, r, "Wake me up at 7 a.m.")
		say(t, r, "Remind me to call my mom in 2 hours.")
		if got := say(t, r, "Cancel the timer."); got != "You have 2: your timer for 10 minutes and your timer for 1 hour. Which one?" {
			t.Errorf("ambiguous: %q", got)
		}
		if got := say(t, r, "Cancel the 10 minute timer."); got != "Cancelled your timer for 10 minutes." {
			t.Errorf("by duration: %q", got)
		}
		if got := say(t, r, "Cancel my 7 o'clock alarm."); got != "Cancelled your alarm for 07:00." {
			t.Errorf("by time: %q", got)
		}
		if got := say(t, r, "Cancel the reminder to call my mom."); got != "Cancelled your reminder to call your mom." {
			t.Errorf("by message: %q", got)
		}
		if got := say(t, r, "Cancel the reminder to buy milk."); got != "You have no reminders." {
			t.Errorf("no match: %q", got)
		}

		say(t, r, "Set a timer for 5 minutes.")
		if got := say(t, r, "Cancel all timers."); got != "Cancelled all 2." {
			t.Errorf("all: %q", got)
		}
		if n := len(timers.list()); n != 0 {
			t.Errorf("%d left", n)
		}
	})
}

func TestCancelAnswerNarrowsTheQuestion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, timers, _ := newManaged(t)
		say(t, r, "Set a timer for 10 minutes.")
		say(t, r, "Set a timer for 1 hour.")
		say(t, r, "Wake me up at 7 a.m.")
		say(t, r, "Remind me to call my mom in 2 hours.")
		resp, err := r.Dispatch(context.Background(), recognize(t, r, "Cancel the timer."))
		if err != nil || resp.Ask == nil {
			t.Fatalf("cancel the timer: %+v, %v; want a question", resp, err)
		}
		// "All of them" means both timers, not the alarm and the reminder.
		answer, err := assistant.SetSlot(resp.Ask.Intent, resp.Ask.Slot, "all of them")
		if err != nil {
			t.Fatal(err)
		}
		if resp, err := r.Dispatch(context.Background(), answer); err != nil || resp.Text != "Cancelled all 2." {
			t.Errorf("all of them: %q, %v; want Cancelled all 2.", resp.Text, err)
		}
		var left []Kind
		for _, e := range timers.list() {
			left = append(left, e.Kind)
		}
		if !slices.Equal(left, []Kind{KindAlarm, KindReminder}) && !slices.Equal(left, []Kind{KindReminder, KindAlarm}) {
			t.Errorf("left: %v, want the alarm and the reminder", left)
		}
	})
}

func TestCancelNoMatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, _, _ := newManaged(t)
		say(t, r, "Remind me to call my mom in 2 hours.")
		want := "Sorry, I couldn't find that one. Your reminder to call your mom is in 2 hours."
		if got := say(t, r, "Cancel the reminder to buy milk."); got != want {
			t.Errorf("%q, want %q", got, want)
		}
	})
}

func TestCancelRingingTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, timers, out := newManaged(t)
		say(t, r, "Set a timer for 1 minute.")
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := say(t, r, "Cancel the timer."); got != "Cancelled your timer for 1 minute." {
			t.Errorf("%q", got)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if chimes, _ := out.counts(); chimes != 1 || len(timers.list()) != 0 {
			t.Errorf("%d chimes, %d left; want 1 and none", chimes, len(timers.list()))
		}
	})
}
