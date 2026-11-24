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
		timers := NewTimers(out, newFakeStore(), nil)
		mustAdd(t, timers, Timer{Kind: KindTimer, At: time.Now().Add(time.Minute), Duration: time.Minute})

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
		timers := NewTimers(out, newFakeStore(), nil)
		defer timers.Close()
		mustAdd(t, timers, Timer{Kind: KindAlarm, At: time.Now().Add(time.Hour)})
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
		timers := NewTimers(out, newFakeStore(), nil)
		defer timers.Close()
		mustAdd(t, timers, Timer{Kind: KindReminder, At: time.Now().Add(time.Minute), Duration: time.Minute, Message: "call your mom"})
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
		timers := NewTimers(out, newFakeStore(), nil)
		mustAdd(t, timers, Timer{Kind: KindAlarm, At: time.Now().Add(time.Minute)})
		mustAdd(t, timers, Timer{Kind: KindAlarm, At: time.Now().Add(time.Hour)})
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
		timers := NewTimers(out, newFakeStore(), nil)
		defer timers.Close()
		mustAdd(t, timers, Timer{Kind: KindAlarm, At: time.Now().Add(time.Hour)})

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

func TestStoreFollowsTimers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newFakeStore()
		timers := NewTimers(&fakeAnnouncer{}, store, nil)
		defer timers.Close()
		goesOff := mustAdd(t, timers, Timer{Kind: KindAlarm, At: time.Now().Add(time.Minute)})
		cancelled := mustAdd(t, timers, Timer{Kind: KindAlarm, At: time.Now().Add(time.Hour)})
		mustAdd(t, timers, Timer{Kind: KindAlarm, At: time.Now().Add(2 * time.Hour)})
		if goesOff.ID == 0 || store.len() != 3 {
			t.Fatalf("after adding: ID %d, %d saved", goesOff.ID, store.len())
		}

		time.Sleep(time.Minute) // goes off: deleted as it starts ringing
		synctest.Wait()
		if store.len() != 2 {
			t.Errorf("after going off: %d saved, want 2", store.len())
		}
		timers.cancel(cancelled.ID)
		if store.len() != 1 {
			t.Errorf("after cancel: %d saved, want 1", store.len())
		}
		timers.Close() // shutdown keeps the pending one for the next start
		if store.len() != 1 {
			t.Errorf("after Close: %d saved, want 1", store.len())
		}
	})
}

func TestRestore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		store := newFakeStore()
		for _, tm := range []Timer{
			{Kind: KindTimer, At: now.Add(10 * time.Minute), Duration: 15 * time.Minute},   // still pending
			{Kind: KindReminder, At: now.Add(-30 * time.Minute), Message: "call your mom"}, // missed, goes off late
			{Kind: KindAlarm, At: now.Add(-2 * time.Hour)},                                 // missed too long ago
		} {
			store.Add(context.Background(), tm)
		}

		out := &fakeAnnouncer{}
		timers := NewTimers(out, store, nil)
		defer timers.Close()
		if err := timers.Restore(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if _, said := out.counts(); len(said) != 1 || said[0] != "Reminder: call your mom." {
			t.Errorf("right after restore said %q; want the late reminder", said)
		}
		if n := len(timers.list()); n != 2 {
			t.Errorf("%d entries, want 2 (pending timer, ringing reminder)", n)
		}
		if store.len() != 1 {
			t.Errorf("%d saved, want 1 (the dropped and the ringing ones are gone)", store.len())
		}
		if _, err := timers.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}

		time.Sleep(10 * time.Minute)
		synctest.Wait()
		if _, said := out.counts(); len(said) != 2 || said[1] != "Your timer for 15 minutes is done." {
			t.Errorf("after 10 minutes said %q; want the restored timer too", said)
		}
	})
}
