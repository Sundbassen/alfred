package clock

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Announcer is how timers get attention when they go off: app.Output, which
// plays one thing at a time.
type Announcer interface {
	Chime(ctx context.Context) error
	Speak(ctx context.Context, text string) error
}

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

// Ringing: chime (and speech) every ringEvery until stopped, for at most
// ringFor.
const (
	ringEvery = 10 * time.Second
	ringFor   = 5 * time.Minute
)

// Timers holds the pending and ringing timers, alarms and reminders, in
// memory (they do not survive a restart yet).
type Timers struct {
	out    Announcer
	logger *slog.Logger

	mu      sync.Mutex
	nextID  int
	entries map[int]*entry
}

// entry is one timer, alarm or reminder.
type entry struct {
	id       int
	kind     Kind
	at       time.Time
	duration time.Duration // for timers: what was asked for
	message  string        // for reminders, in the second person

	timer    *time.Timer        // fires at `at`
	stopRing context.CancelFunc // set while ringing
}

// NewTimers returns an empty set of timers announcing through out.
func NewTimers(out Announcer, logger *slog.Logger) *Timers {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Timers{out: out, logger: logger, entries: make(map[int]*entry)}
}

// add schedules a timer, alarm or reminder.
func (t *Timers) add(kind Kind, w when, message string) *entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	e := &entry{id: t.nextID, kind: kind, at: w.At, duration: w.Duration, message: message}
	t.entries[e.id] = e
	e.timer = time.AfterFunc(time.Until(w.At), func() { t.fire(e.id) })
	t.logger.Info("Timer set", slog.String("kind", kind.String()), slog.Time("at", w.At),
		slog.String("message", message))
	return e
}

// fire rings an entry until it is stopped or ringFor passes, then removes it.
func (t *Timers) fire(id int) {
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

	t.logger.Info("Timer going off", slog.String("kind", e.kind.String()), slog.String("message", e.message))
	t.ring(ctx, e)

	t.mu.Lock()
	delete(t.entries, id)
	t.mu.Unlock()
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
			t.logger.Info("Timer gave up ringing", slog.String("kind", e.kind.String()))
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

// Close cancels every pending timer and stops every ringing one.
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

// ringText is what to say while ringing: nothing for an alarm (the chime
// says it).
func (e *entry) ringText() string {
	switch e.kind {
	case KindReminder:
		return "Reminder: " + e.message + "."
	case KindTimer:
		return "Your timer for " + formatDuration(e.duration) + " is done."
	default:
		return ""
	}
}
