package clock

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/spoken"
)

// Announcer is how timers get attention when they go off: app.Output, which
// plays one thing at a time.
type Announcer interface {
	Chime(ctx context.Context) error
	Speak(ctx context.Context, text string) error
}

// Ringing: chime (and speech) every ringEvery until stopped, for at most
// ringFor.
const (
	ringEvery = 10 * time.Second
	ringFor   = 5 * time.Minute
)

// Store keeps timers across restarts (clockstore, in the local database).
type Store interface {
	// Add saves a timer and returns it with its ID.
	Add(ctx context.Context, t Timer) (Timer, error)
	// Delete removes a timer; deleting one that is gone is not an error.
	Delete(ctx context.Context, id int64) error
	// Pending returns every saved timer.
	Pending(ctx context.Context) ([]Timer, error)
}

// maxMissed is how overdue a timer may be at startup (Alfred was down when
// it was due) and still go off, late; older ones are dropped.
const maxMissed = time.Hour

// storeTimeout bounds store calls made in the background.
const storeTimeout = 5 * time.Second

// Timers holds the pending and ringing timers, alarms and reminders. The
// pending ones are saved in the Store, so they survive a restart (Restore);
// one that goes off or is cancelled is deleted from it.
type Timers struct {
	out    Announcer
	store  Store
	logger *slog.Logger

	mu      sync.Mutex
	entries map[int64]*entry
}

// entry is a Timer plus its runtime state.
type entry struct {
	Timer

	timer    *time.Timer        // fires at At
	stopRing context.CancelFunc // set while ringing
}

// NewTimers returns an empty set of timers announcing through out and saved
// in store. Call Restore once to bring back the saved ones.
func NewTimers(out Announcer, store Store, logger *slog.Logger) *Timers {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Timers{out: out, store: store, logger: logger, entries: make(map[int64]*entry)}
}

// Restore schedules the saved timers. One that came due while Alfred was
// down goes off now if it is at most maxMissed overdue; older ones are
// deleted with a log line.
func (t *Timers) Restore(ctx context.Context) error {
	saved, err := t.store.Pending(ctx)
	if err != nil {
		return errors.WithMessage(err, "restore timers")
	}
	now := time.Now()
	for _, tm := range saved {
		if overdue := now.Sub(tm.At); overdue > maxMissed {
			t.logger.Warn("Dropping a timer missed while Alfred was down", slog.String("kind", tm.Kind.String()),
				slog.Time("at", tm.At), slog.String("message", tm.Message))
			if err := t.store.Delete(ctx, tm.ID); err != nil {
				return errors.WithMessage(err, "drop missed timer")
			}
			continue
		} else if overdue > 0 {
			t.logger.Info("A timer was due while Alfred was down; going off late", slog.String("kind", tm.Kind.String()),
				slog.Duration("late", overdue))
		}
		t.schedule(tm)
	}
	if len(saved) > 0 {
		t.logger.Info("Restored timers", slog.Int("count", len(saved)))
	}
	return nil
}

// add saves and schedules a timer, alarm or reminder.
func (t *Timers) add(ctx context.Context, tm Timer) (*entry, error) {
	tm, err := t.store.Add(ctx, tm)
	if err != nil {
		return nil, errors.WithMessage(err, "save timer")
	}
	t.logger.Info("Timer set", slog.String("kind", tm.Kind.String()), slog.Time("at", tm.At),
		slog.String("message", tm.Message))
	return t.schedule(tm), nil
}

// schedule arms a saved timer.
func (t *Timers) schedule(tm Timer) *entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := &entry{Timer: tm}
	t.entries[tm.ID] = e
	e.timer = time.AfterFunc(time.Until(tm.At), func() { t.fire(tm.ID) })
	return e
}

// fire rings an entry until it is stopped or ringFor passes, then removes
// it. It is deleted from the store as it goes off, so a restart while it
// rings does not ring it again.
func (t *Timers) fire(id int64) {
	t.mu.Lock()
	e, ok := t.entries[id]
	if !ok {
		t.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.stopRing = cancel
	t.mu.Unlock()

	t.logger.Info("Timer going off", slog.String("kind", e.Kind.String()), slog.String("message", e.Message))
	t.forget(id)
	t.ring(ctx, e)

	t.mu.Lock()
	if t.entries[id] == e { // not cancelled and replaced meanwhile
		delete(t.entries, id)
	}
	t.mu.Unlock()
}

// forget deletes a timer from the store, logging a failure: the timer has
// gone off or been cancelled either way, so the caller carries on.
func (t *Timers) forget(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if err := t.store.Delete(ctx, id); err != nil {
		t.logger.Error("Failed to delete timer from the store", slog.Int64("id", id), slog.String("error", err.Error()))
	}
}

func (t *Timers) ring(ctx context.Context, e *entry) {
	text := e.ringText()
	deadline := time.Now().Add(ringFor)
	for {
		start := time.Now()
		if err := t.out.Chime(ctx); err != nil && ctx.Err() == nil {
			t.logger.Warn("Timer chime failed", slog.String("error", err.Error()))
		}
		if text != "" && ctx.Err() == nil {
			if err := t.out.Speak(ctx, text); err != nil && ctx.Err() == nil {
				t.logger.Warn("Timer announcement failed", slog.String("error", err.Error()))
			}
		}
		next := start.Add(ringEvery)
		if !next.Before(deadline) {
			t.logger.Info("Timer gave up ringing", slog.String("kind", e.Kind.String()))
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
	}
}

// Stop stops every ringing timer, alarm and reminder, reporting whether one
// was ringing (a stop.Stopper).
func (t *Timers) Stop(context.Context) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	stopped := false
	for _, e := range t.entries {
		if e.stopRing != nil {
			e.stopRing()
			stopped = true
		}
	}
	return stopped, nil
}

// list returns copies of the entries, soonest first.
func (t *Timers) list() []entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	entries := make([]entry, 0, len(t.entries))
	for _, e := range t.entries {
		entries = append(entries, *e)
	}
	slices.SortFunc(entries, func(a, b entry) int { return a.At.Compare(b.At) })
	return entries
}

// cancel cancels the entries with the given ids: pending ones never go
// off (and are deleted from the store), ringing ones stop.
func (t *Timers) cancel(ids ...int64) {
	var pending []int64
	t.mu.Lock()
	for _, id := range ids {
		e, ok := t.entries[id]
		if !ok {
			continue
		}
		e.timer.Stop()
		delete(t.entries, id)
		if e.stopRing != nil {
			e.stopRing() // already gone from the store, deleted as it went off
			continue
		}
		pending = append(pending, id)
	}
	t.mu.Unlock()
	for _, id := range pending {
		t.forget(id)
	}
}

// snooze stops every ringing timer, alarm and reminder and sets it again,
// d from now. It returns how many it snoozed. Each is saved again before
// the ringing one is cancelled, so a failed save leaves it ringing rather
// than lost (ids are never reused, see the timers migration).
func (t *Timers) snooze(ctx context.Context, d time.Duration) (int, error) {
	t.mu.Lock()
	var ringing []Timer
	for _, e := range t.entries {
		if e.ringing() {
			ringing = append(ringing, e.Timer)
		}
	}
	t.mu.Unlock()
	at := time.Now().Add(d)
	for i, tm := range ringing {
		old := tm.ID
		tm.At = at
		if _, err := t.add(ctx, tm); err != nil {
			return i, err
		}
		t.cancel(old)
	}
	return len(ringing), nil
}

// extend adds d to a timer: to when it goes off, or, if it is ringing, d
// from now (it is then a timer for d: "your timer for 3 minutes"). It
// returns the timer as rescheduled. The new one is saved before the old
// one is cancelled, so a failed save keeps the old one.
func (t *Timers) extend(ctx context.Context, id int64, d time.Duration) (*entry, error) {
	t.mu.Lock()
	e, ok := t.entries[id]
	if !ok {
		t.mu.Unlock()
		return nil, errors.WithMessagef(fault.ErrNotFound, "timer %d", id)
	}
	tm := e.Timer
	ringing := e.ringing()
	t.mu.Unlock()

	tm.At = tm.At.Add(d)
	switch {
	case ringing:
		tm.At = time.Now().Add(d)
		tm.Duration = d
	case tm.Duration > 0:
		tm.Duration += d
	}
	e, err := t.add(ctx, tm)
	if err != nil {
		return nil, err
	}
	t.cancel(id)
	return e, nil
}

// Close stops every timer for shutdown: pending ones stay in the store, to
// be restored at the next start; ringing ones stop.
func (t *Timers) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, e := range t.entries {
		e.timer.Stop()
		if e.stopRing != nil {
			e.stopRing()
		}
		delete(t.entries, id)
	}
}

// ringing reports whether the entry is going off.
func (e *entry) ringing() bool { return e.stopRing != nil }

// ringText is what to say while ringing: nothing for an alarm (the chime
// says it).
func (e *entry) ringText() string {
	switch e.Kind {
	case KindReminder:
		return "Reminder: " + e.Message + "."
	case KindTimer:
		return "Your timer for " + spoken.FormatDuration(e.Duration) + " is done."
	default:
		return ""
	}
}
