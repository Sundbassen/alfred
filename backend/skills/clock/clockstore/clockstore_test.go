package clockstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/store"
	"github.com/sundbassen/alfred/skills/clock"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "alfred.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	at := time.Date(2026, 11, 24, 19, 0, 0, 0, time.Local)
	timers := []clock.Timer{
		{Kind: clock.KindReminder, At: at, Message: "call your mom"},
		{Kind: clock.KindTimer, At: at.Add(-time.Hour), Duration: 10 * time.Minute},
		{Kind: clock.KindAlarm, At: at.Add(time.Hour)},
	}
	for i, tm := range timers {
		saved, err := s.Add(ctx, tm)
		if err != nil {
			t.Fatal(err)
		}
		if saved.ID == 0 {
			t.Errorf("timer %d got no ID", i)
		}
		timers[i] = saved
	}

	got, err := s.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []clock.Timer{timers[1], timers[0], timers[2]} // soonest first
	if len(got) != len(want) {
		t.Fatalf("Pending = %+v", got)
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Kind != want[i].Kind || !got[i].At.Equal(want[i].At) ||
			got[i].Duration != want[i].Duration || got[i].Message != want[i].Message {
			t.Errorf("Pending[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	if err := s.Delete(ctx, timers[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, timers[0].ID); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
	if got, _ := s.Pending(ctx); len(got) != 2 {
		t.Errorf("after delete: %+v", got)
	}
}

func TestUnknownKind(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.Add(ctx, clock.Timer{Kind: clock.Kind(42), At: time.Now()}); !errors.Is(err, fault.ErrInvalidValue) {
		t.Errorf("Add: %v, want ErrInvalidValue", err)
	}
}
