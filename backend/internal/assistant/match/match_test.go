package match

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"gitlab.com/tozd/go/errors"
)

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{" What time is it?", "what time is it"},
		{"What's the time?", "what is the time"},
		{"What’s the time?", "what is the time"},
		{"Hey Alfred, uh, tell me the time please.", "tell me the time"},
		{"Turn on the living-room lights!", "turn on the living room lights"},
		{"Set the volume to 5%.", "set the volume to 5 percent"},
		{"  ", ""},
		{"...", ""},
	}
	for _, tt := range tests {
		if got := Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func routes(intents ...assistant.Route) []assistant.Route { return intents }

func route(name string, phrases ...string) assistant.Route {
	return assistant.Route{Name: name, Spec: assistant.Spec{Phrases: phrases}}
}

func newTestMatcher() *Matcher {
	return New(routes(
		route("time", "what time is it", "what is the time"),
		route("lights_on", "turn on the lights"),
	))
}

func TestRecognize(t *testing.T) {
	m := newTestMatcher()
	tests := []struct {
		transcript string
		wantName   string // "" means no match
		wantExact  bool
	}{
		{" What time is it?", "time", true},
		{"What's the time?", "time", true},
		{"what time is it now", "time", false},
		{"turn on the lights", "lights_on", true},
		{"tell me a joke", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		res, err := m.Recognize(context.Background(), tt.transcript)
		if tt.wantName == "" {
			if !errors.Is(err, fault.ErrNotFound) {
				t.Errorf("Recognize(%q) = %+v, %v; want fault.ErrNotFound", tt.transcript, res, err)
			}
			continue
		}
		if err != nil || res.Intent != tt.wantName {
			t.Errorf("Recognize(%q) = %+v, %v; want %s", tt.transcript, res, err, tt.wantName)
			continue
		}
		if tt.wantExact && res.Score != 1 {
			t.Errorf("Recognize(%q) score = %.2f, want 1", tt.transcript, res.Score)
		}
	}
}

func TestPrompt(t *testing.T) {
	m := New(routes(
		route("time", "What time is it", "what is the time"),
		route("clock", "what time is it"), // duplicate phrase
	))
	if got, want := m.Prompt(), "What time is it? What is the time?"; got != want {
		t.Errorf("Prompt() = %q, want %q", got, want)
	}

	if got, want := New(routes(route("lights", "turn on the lights"))).Prompt(), "Turn on the lights."; got != want {
		t.Errorf("Prompt() = %q, want %q", got, want)
	}

	if got := New(nil).Prompt(); got != "" {
		t.Errorf("empty matcher Prompt() = %q, want empty", got)
	}

	// Templates: the literal words before the first slot, without trailing
	// function words; fragments of only question or function words are
	// left out.
	tm := New(routes(
		route("timer", "set a timer for {when}", "set a {when} timer"),
		route("list", "what is on the {list}", "add {items} to the {list}"),
		route("play", "play {query}", "play some {query}"),
	))
	if got, want := tm.Prompt(), "Set a timer. Add. Play."; got != want {
		t.Errorf("templates: Prompt() = %q, want %q", got, want)
	}

	var many []string
	for i := range 100 {
		many = append(many, strings.Repeat("word ", 3)+strconv.Itoa(i))
	}
	words := len(strings.Fields(New(routes(route("many", many...))).Prompt()))
	if words > MaxPromptWords {
		t.Errorf("long Prompt() has %d words, want at most %d", words, MaxPromptWords)
	}
}

func TestPromptIsFair(t *testing.T) {
	// An intent with many phrases registered first must not crowd out the
	// intents after it.
	var many []string
	for i := range 100 {
		many = append(many, "turn on lamp number "+strconv.Itoa(i))
	}
	p := New(routes(route("lamps", many...), route("time", "what time is it"), route("weather", "what is the weather"))).Prompt()
	for _, want := range []string{"Turn on lamp number 0.", "What time is it?", "What is the weather?"} {
		if !strings.Contains(p, want) {
			t.Errorf("Prompt() lacks %q:\n%s", want, p)
		}
	}
	if words := len(strings.Fields(p)); words > MaxPromptWords {
		t.Errorf("%d words, want at most %d", words, MaxPromptWords)
	}
}

func TestPromptText(t *testing.T) {
	for _, tc := range []struct{ phrase, want string }{
		{"What time is it", "what time is it"},
		{"set a timer for {when}", "set a timer"},
		{"put on {query}", "put on"},
		{"turn off the {light}", "turn off"},
		{"play {query} in the {speaker}", "play"},
		{"what is on the {list}", ""},
		{"add {items}", "add"},
	} {
		if got := PromptText(tc.phrase); got != tc.want {
			t.Errorf("PromptText(%q) = %q, want %q", tc.phrase, got, tc.want)
		}
	}
}

func TestPromptDropsRepeatedFragments(t *testing.T) {
	// "Set." only repeats the start of "Set a timer."; a plain phrase is
	// kept even when it is the start of another ("Stop." and "Stop the
	// music." are both commands).
	m := New(routes(
		route("timer", "set a timer for {when}"),
		route("alarm", "set {when}"),
		route("stop", "stop", "stop the music"),
	))
	if got, want := m.Prompt(), "Set a timer. Stop. Stop the music."; got != want {
		t.Errorf("Prompt() = %q, want %q", got, want)
	}
	// A fragment that is also a plain phrase stays, whichever comes first.
	m = New(routes(
		route("play", "play {query}", "play a radio with {artists}"),
		route("resume", "play"),
	))
	if got, want := m.Prompt(), "Play. Play a radio."; got != want {
		t.Errorf("Prompt() = %q, want %q", got, want)
	}
}

func TestNoMatchErrorCarriesBest(t *testing.T) {
	_, err := newTestMatcher().Recognize(context.Background(), "tell me a joke about time")
	var noMatch *NoMatchError
	if !errors.As(err, &noMatch) || noMatch.Best.Intent != "time" || noMatch.Best.Score == 0 {
		t.Errorf("Recognize = %v; want a *NoMatchError with best candidate time", err)
	}
}

func TestTemplates(t *testing.T) {
	m := New(routes(
		route("time", "what time is it"),
		route("play", "play {query}", "put on {query}"),
		route("play_in", "play {query} in the {speaker}"),
		route("mix", "play a radio with {artists}"),
	))
	tests := []struct {
		transcript string
		wantName   string
		wantSlots  map[string]string
	}{
		{"Hey Alfred, play Daft Punk.", "play", map[string]string{"query": "daft punk"}},
		{"put on some jazz please", "play", map[string]string{"query": "some jazz"}},
		{"play jazz in the kitchen", "play_in", map[string]string{"query": "jazz", "speaker": "kitchen"}},
		// The query takes everything up to the last "in the".
		{"play songs in the key of life in the kitchen", "play_in",
			map[string]string{"query": "songs in the key of life", "speaker": "kitchen"}},
		{"Play a radio with Daft Punk and Justice.", "mix", map[string]string{"artists": "daft punk and justice"}},
		{"what time is it", "time", nil},
	}
	for _, tt := range tests {
		res, err := m.Recognize(context.Background(), tt.transcript)
		if err != nil || res.Intent != tt.wantName {
			t.Errorf("Recognize(%q) = %+v, %v; want %s", tt.transcript, res, err, tt.wantName)
			continue
		}
		if len(res.Slots) != len(tt.wantSlots) {
			t.Errorf("Recognize(%q) slots = %v, want %v", tt.transcript, res.Slots, tt.wantSlots)
			continue
		}
		for k, v := range tt.wantSlots {
			if res.Slots[k] != v {
				t.Errorf("Recognize(%q) slot %s = %q, want %q", tt.transcript, k, res.Slots[k], v)
			}
		}
	}
}

func TestTemplateNeedsSlotWords(t *testing.T) {
	m := New(routes(route("play", "play {query}")))
	for _, transcript := range []string{"play", "stop the music"} {
		if _, err := m.Recognize(context.Background(), transcript); !errors.Is(err, fault.ErrNotFound) {
			t.Errorf("Recognize(%q) error = %v, want no match", transcript, err)
		}
	}
}

func TestPromptTemplates(t *testing.T) {
	m := New(routes(route("play", "play {query}", "play a radio with {artists}"), route("pause", "pause")))
	// The fragment "Play." goes, since "Play a radio." starts with it, so
	// play's first sentence is "Play a radio."
	if got, want := m.Prompt(), "Play a radio. Pause."; got != want {
		t.Errorf("Prompt() = %q, want %q", got, want)
	}
}

func TestExactPhraseBeatsTemplate(t *testing.T) {
	m := New(routes(route("play", "play {query}"), route("radio", "play the radio")))
	res, err := m.Recognize(context.Background(), "play the radio")
	if err != nil || res.Intent != "radio" || res.Slots != nil {
		t.Errorf("Recognize = %+v, %v; want the exact phrase radio", res, err)
	}
}

func TestSlotsKeepCommas(t *testing.T) {
	m := New(routes(route("add", "add {items} to the {list}")))
	rec, err := m.Recognize(context.Background(), "Add milk, eggs, and bread to the shopping list, please.")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Slots["items"] != "milk, eggs, and bread" || rec.Slots["list"] != "shopping list" {
		t.Errorf("slots = %q", rec.Slots)
	}
	if got := Normalize("Milk, eggs, and bread."); got != "milk eggs and bread" {
		t.Errorf("Normalize drops commas: %q", got)
	}
}
