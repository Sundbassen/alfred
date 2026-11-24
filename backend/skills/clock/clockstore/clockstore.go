// Package clockstore keeps the clock skill's timers, alarms and reminders
// in Alfred's local database (internal/store). It only runs the generated
// queries (internal/store/storedb, from internal/store/queries/timers.sql)
// and converts between their rows and the skill's domain type,
// clock.Timer.
package clockstore

import (
	"context"
	"database/sql"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/store/storedb"
	"github.com/sundbassen/alfred/skills/clock"
)

// Store keeps timers in the database.
type Store struct {
	q *storedb.Queries
}

// New returns a Store over a database opened by store.Open.
func New(db *sql.DB) *Store {
	return &Store{q: storedb.New(db)}
}

// Add saves t and returns it with its ID.
func (s *Store) Add(ctx context.Context, t clock.Timer) (clock.Timer, error) {
	kind, ok := kindToDB[t.Kind]
	if !ok {
		return clock.Timer{}, errors.WithMessagef(fault.ErrInvalidValue, "timer kind %d", t.Kind)
	}
	id, err := s.q.AddTimer(ctx, storedb.AddTimerParams{
		Kind:     kind,
		At:       t.At.UnixMilli(),
		Duration: t.Duration.Milliseconds(),
		Message:  t.Message,
	})
	if err != nil {
		return clock.Timer{}, errors.WithMessage(err, "add timer")
	}
	t.ID = id
	return t, nil
}

// Delete removes the timer with the given ID (no error if it is gone).
func (s *Store) Delete(ctx context.Context, id int64) error {
	return errors.WithMessagef(s.q.DeleteTimer(ctx, id), "delete timer %d", id)
}

// Pending returns every saved timer, soonest first.
func (s *Store) Pending(ctx context.Context) ([]clock.Timer, error) {
	rows, err := s.q.PendingTimers(ctx)
	if err != nil {
		return nil, errors.WithMessage(err, "list timers")
	}
	timers := make([]clock.Timer, 0, len(rows))
	for _, row := range rows {
		kind, ok := kindFromDB[row.Kind]
		if !ok {
			return nil, errors.WithMessagef(fault.ErrDecoding, "timer %d: unknown kind %q", row.ID, row.Kind)
		}
		timers = append(timers, clock.Timer{
			ID:       row.ID,
			Kind:     kind,
			At:       time.UnixMilli(row.At),
			Duration: time.Duration(row.Duration) * time.Millisecond,
			Message:  row.Message,
		})
	}
	return timers, nil
}

// The kinds as stored: stable names, independent of clock.Kind's values.
var (
	kindToDB = map[clock.Kind]string{
		clock.KindTimer:    "timer",
		clock.KindAlarm:    "alarm",
		clock.KindReminder: "reminder",
	}
	kindFromDB = map[string]clock.Kind{
		"timer":    clock.KindTimer,
		"alarm":    clock.KindAlarm,
		"reminder": clock.KindReminder,
	}
)
