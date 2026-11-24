package clock

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

// fakeAnnouncer records chimes and speech.
type fakeAnnouncer struct {
	mu     sync.Mutex
	chimes int
	said   []string
}

func (f *fakeAnnouncer) Chime(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chimes++
	return nil
}

func (f *fakeAnnouncer) Speak(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.said = append(f.said, text)
	return nil
}

func (f *fakeAnnouncer) counts() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.chimes, append([]string(nil), f.said...)
}

// fakeStore keeps timers in memory, like clockstore does in the database.
type fakeStore struct {
	mu     sync.Mutex
	nextID int64
	timers map[int64]Timer
	err    error // returned by Add
}

func newFakeStore() *fakeStore { return &fakeStore{timers: make(map[int64]Timer)} }

func (s *fakeStore) Add(_ context.Context, t Timer) (Timer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return Timer{}, s.err
	}
	s.nextID++
	t.ID = s.nextID
	s.timers[t.ID] = t
	return t, nil
}

func (s *fakeStore) Delete(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.timers, id)
	return nil
}

func (s *fakeStore) Pending(context.Context) ([]Timer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ts []Timer
	for _, t := range s.timers {
		ts = append(ts, t)
	}
	slices.SortFunc(ts, func(a, b Timer) int { return a.At.Compare(b.At) })
	return ts, nil
}

func (s *fakeStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.timers)
}

// mustAdd saves and schedules a timer.
func mustAdd(t *testing.T, timers *Timers, tm Timer) *entry {
	t.Helper()
	e, err := timers.add(context.Background(), tm)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestAskTime(t *testing.T) {
	r := assistant.NewRouter()
	register(r, NewTimers(&fakeAnnouncer{}, newFakeStore(), nil), func() time.Time { return time.Date(2026, 11, 23, 9, 5, 0, 0, time.Local) })

	resp, err := r.Dispatch(context.Background(), AskTime{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "It's 09:05."; resp.Text != want {
		t.Errorf("Text = %q, want %q", resp.Text, want)
	}
}

// recognize runs a transcript through the templates and fills the intent.
func recognize(t *testing.T, r *assistant.Router, transcript string) assistant.Intent {
	t.Helper()
	rec, err := match.New(r.Intents()).Recognize(context.Background(), transcript)
	if err != nil {
		t.Fatalf("Recognize(%q): %v", transcript, err)
	}
	intent, err := r.New(rec.Intent)
	if err != nil {
		t.Fatal(err)
	}
	intent, err = assistant.Fill(intent, rec.Slots)
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

func TestPhrasesAreRecognized(t *testing.T) {
	r := assistant.NewRouter()
	timers := NewTimers(&fakeAnnouncer{}, newFakeStore(), nil)
	defer timers.Close()
	Register(r, timers)
	tests := map[string]assistant.Intent{
		" What time is it?":                          AskTime{},
		"Hey Alfred, what's the time?":               AskTime{},
		"Tell me the time, please.":                  AskTime{},
		"Set a timer for 10 minutes.":                SetTimer{When: "10 minutes"},
		"Set a 10 minute timer.":                     SetTimer{When: "10 minute"},
		"Set an alarm for 7:30 a.m.":                 SetTimer{When: "7 30 a m"},
		"Wake me up at 7.":                           SetTimer{When: "7"},
		"Remind me to call my mom in 10 minutes.":    SetTimer{Message: "call my mom in 10 minutes"},
		"Remind me in 10 minutes to go to the shop.": SetTimer{Message: "in 10 minutes to go to the shop"},
	}
	for transcript, want := range tests {
		if got := recognize(t, r, transcript); got != want {
			t.Errorf("%q: %#v, want %#v", transcript, got, want)
		}
	}
}

// at10 is the "now" of the handler tests: Monday 23 Nov 2026, 10:00.
var at10 = time.Date(2026, 11, 23, 10, 0, 0, 0, time.Local)

func TestSetTimerReplies(t *testing.T) {
	tests := map[string]string{
		"Set a timer for 10 minutes.":                 "Timer set for 10 minutes.",
		"Set a timer for an hour and a half.":         "Timer set for 1 hour and 30 minutes.",
		"Wake me up at 7 a.m.":                        "Alarm set for 07:00 tomorrow.",
		"Wake me up at 7.":                            "Alarm set for 19:00.",
		"Set an alarm for 7:30 p.m.":                  "Alarm set for 19:30.",
		"Remind me to call my mom in 10 minutes.":     "OK, I'll remind you to call your mom in 10 minutes.",
		"Remind me in 10 minutes to go to the shop.":  "OK, I'll remind you to go to the shop in 10 minutes.",
		"Remind me to look at the oven in 5 minutes.": "OK, I'll remind you to look at the oven in 5 minutes.",
		"Remind me to call in sick tomorrow at 8.":    "OK, I'll remind you to call in sick at 08:00 tomorrow.",
		"Remind me at 7 to take my pills.":            "OK, I'll remind you to take your pills at 19:00.",
	}
	for transcript, want := range tests {
		r := assistant.NewRouter()
		timers := NewTimers(&fakeAnnouncer{}, newFakeStore(), nil)
		register(r, timers, func() time.Time { return at10 })
		resp, err := r.Dispatch(context.Background(), recognize(t, r, transcript))
		timers.Close()
		if err != nil || resp.Text != want {
			t.Errorf("%q: %q, %v; want %q", transcript, resp.Text, err, want)
		}
	}
}

func TestSetTimerErrors(t *testing.T) {
	tests := map[string]string{
		"Remind me to buy milk.":           "Sorry, I didn't get when.",
		"Set a timer for banana.":          "Sorry, I didn't get when.",
		"Set a timer for 30 hours.":        "Sorry, I can only set timers for up to 24 hours.",
		"Remind me to run in the morning.": "Sorry, I didn't get when.",
	}
	for transcript, want := range tests {
		r := assistant.NewRouter()
		register(r, NewTimers(&fakeAnnouncer{}, newFakeStore(), nil), func() time.Time { return at10 })
		_, err := r.Dispatch(context.Background(), recognize(t, r, transcript))
		if got := assistant.SpokenText(err); got != want {
			t.Errorf("%q: %q (%v), want %q", transcript, got, err, want)
		}
	}
}

func TestSetTimerStoreFailure(t *testing.T) {
	r := assistant.NewRouter()
	store := newFakeStore()
	store.err = errors.WithMessage(fault.ErrInternal, "disk full")
	register(r, NewTimers(&fakeAnnouncer{}, store, nil), func() time.Time { return at10 })
	_, err := r.Dispatch(context.Background(), recognize(t, r, "Set a timer for 10 minutes."))
	if !errors.Is(err, fault.ErrInternal) || assistant.SpokenText(err) != assistant.FailedText {
		t.Errorf("err = %v (%q)", err, assistant.SpokenText(err))
	}
}

func TestRecognizerFilledBothSlots(t *testing.T) {
	// The model tier may put the time in When and also leave it in Message.
	r := assistant.NewRouter()
	timers := NewTimers(&fakeAnnouncer{}, newFakeStore(), nil)
	defer timers.Close()
	register(r, timers, func() time.Time { return at10 })
	resp, err := r.Dispatch(context.Background(), SetTimer{When: "10 minutes", Message: "call mom in 10 minutes"})
	if want := "OK, I'll remind you to call mom in 10 minutes."; err != nil || resp.Text != want {
		t.Errorf("%q, %v; want %q", resp.Text, err, want)
	}
}
