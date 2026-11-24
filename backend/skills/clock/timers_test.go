package clock

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestTimerRingsUntilItGivesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		out := &fakeAnnouncer{}
		timers := NewTimers(out, nil)
		timers.add(KindTimer, when{At: time.Now().Add(time.Minute), Duration: time.Minute}, "")

		time.Sleep(time.Minute - time.Second)
		synctest.Wait()
		if chimes, _ := out.counts(); chimes != 0 {
			t.Fatalf("chimed %d times before it was due", chimes)
		}

		time.Sleep(time.Second)
		synctest.Wait()
		chimes, said := out.counts()
		if chimes != 1 || len(said) != 1 || said[0] != "Your timer for 1 minute is done." {
			t.Fatalf("when due: %d chimes, said %q", chimes, said)
		}

		time.Sleep(ringEvery)
		synctest.Wait()
		if chimes, _ := out.counts(); chimes != 2 {
			t.Errorf("after %s: %d chimes, want 2", ringEvery, chimes)
		}

		time.Sleep(ringFor)
		synctest.Wait()
		if chimes, _ := out.counts(); chimes != int(ringFor/ringEvery) {
			t.Errorf("after giving up: %d chimes, want %d", chimes, ringFor/ringEvery)
		}
		timers.mu.Lock()
		left := len(timers.entries)
		timers.mu.Unlock()
		if left != 0 {
			t.Errorf("%d entries left after ringing", left)
		}
	})
}

func TestAlarmOnlyChimes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		out := &fakeAnnouncer{}
		timers := NewTimers(out, nil)
		defer timers.Close()
		timers.add(KindAlarm, when{At: time.Now().Add(time.Hour)}, "")
		time.Sleep(time.Hour + ringEvery)
		synctest.Wait()
		if chimes, said := out.counts(); chimes != 2 || len(said) != 0 {
			t.Errorf("%d chimes, said %q; want 2 chimes and nothing said", chimes, said)
		}
	})
}

func TestReminderSaysItsMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		out := &fakeAnnouncer{}
		timers := NewTimers(out, nil)
		defer timers.Close()
		timers.add(KindReminder, when{At: time.Now().Add(time.Minute), Duration: time.Minute}, "call your mom")
		time.Sleep(time.Minute)
		synctest.Wait()
		if _, said := out.counts(); len(said) != 1 || said[0] != "Reminder: call your mom." {
			t.Errorf("said %q", said)
		}
	})
}

func TestCloseStopsEverything(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		out := &fakeAnnouncer{}
		timers := NewTimers(out, nil)
		timers.add(KindAlarm, when{At: time.Now().Add(time.Minute)}, "")
		timers.add(KindAlarm, when{At: time.Now().Add(time.Hour)}, "")
		time.Sleep(time.Minute)
		synctest.Wait()
		timers.Close() // one ringing, one pending
		time.Sleep(2 * time.Hour)
		synctest.Wait()
		if chimes, _ := out.counts(); chimes != 1 {
			t.Errorf("%d chimes, want 1 (before Close)", chimes)
		}
	})
}

func TestStopEndsRinging(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		out := &fakeAnnouncer{}
		timers := NewTimers(out, nil)
		defer timers.Close()
		timers.add(KindAlarm, when{At: time.Now().Add(time.Hour)}, "")

		if stopped, err := timers.Stop(context.Background()); stopped || err != nil {
			t.Errorf("Stop before ringing = %v, %v; want false (only pending)", stopped, err)
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		if stopped, err := timers.Stop(context.Background()); !stopped || err != nil {
			t.Errorf("Stop while ringing = %v, %v; want true", stopped, err)
		}
		time.Sleep(ringFor)
		synctest.Wait()
		timers.mu.Lock()
		left := len(timers.entries)
		timers.mu.Unlock()
		if chimes, _ := out.counts(); chimes != 1 || left != 0 {
			t.Errorf("%d chimes, %d entries left; want 1 chime and none left", chimes, left)
		}
	})
}
