package weather

import (
	"context"
	"testing"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

type fakeForecaster struct {
	f   Forecast
	err error
}

func (f fakeForecaster) Forecast(context.Context) (Forecast, error) { return f.f, f.err }

// now is Tuesday 24 November 2026, 10:00.
var now = time.Date(2026, 11, 24, 10, 0, 0, 0, time.Local)

func forecast() Forecast {
	day := func(d int, high, low float64, rain int, cond string) Day {
		return Day{Date: time.Date(2026, 11, d, 0, 0, 0, 0, time.Local), High: high, Low: low, RainChance: rain, Condition: cond}
	}
	return Forecast{
		Now: Current{Temperature: 7.3, Condition: "cloudy"},
		Days: []Day{
			day(24, 9.4, 2.6, 60, "rainy"),
			day(25, 12, 4, 10, "clear"),
			day(26, 3, -2.6, 30, "light snow"),
			day(27, 5, 0, 0, "partly cloudy"),
			day(28, 6, 1, -1, ""), // no rain chance or condition from the model
		},
	}
}

func respond(t *testing.T, f Forecaster, place, transcript string) string {
	t.Helper()
	r := assistant.NewRouter()
	register(r, f, place, func() time.Time { return now })
	reply, err := app.NewAssistant(r, match.New(r.Intents())).Respond(context.Background(), transcript)
	if err != nil {
		return assistant.SpokenText(err)
	}
	return reply.Text
}

func TestWhichDay(t *testing.T) {
	// An unknown day asks, and the answer completes the question.
	r := assistant.NewRouter()
	register(r, fakeForecaster{f: forecast()}, "", func() time.Time { return now })
	a := app.NewAssistant(r, match.New(r.Intents()))
	for _, step := range [][2]string{
		{"Will it rain banana?", "Which day?"},
		{"Tomorrow.", "Probably not: a 10 percent chance of rain tomorrow."},
	} {
		reply, err := a.Respond(context.Background(), step[0])
		if err != nil || reply.Text != step[1] {
			t.Errorf("%q: %+v, %v; want %q", step[0], reply, err, step[1])
		}
	}
}

func TestWeather(t *testing.T) {
	f := fakeForecaster{f: forecast()}
	for transcript, want := range map[string]string{
		"What's the weather?":                      "It's 7 degrees and cloudy. Today: a high of 9 and a low of 3, with a 60 percent chance of rain.",
		"What's the weather tomorrow?":             "Tomorrow: clear, a high of 12 and a low of 4, with a 10 percent chance of rain.",
		"What's the forecast for Thursday?":        "On Thursday: light snow, a high of 3 and a low of minus 3, with a 30 percent chance of rain.",
		"How's the weather on Friday?":             "On Friday: partly cloudy, a high of 5 and a low of 0.",
		"Will it rain?":                            "Yes, a 60 percent chance of rain today.",
		"Will it rain on Thursday?":                "Maybe: a 30 percent chance of rain on Thursday.",
		"Do I need an umbrella tomorrow?":          "Probably not: a 10 percent chance of rain tomorrow.",
		"What's the weather on Sunday?":            "Sorry, I only have the forecast for the coming week.",
		"What's the weather banana?":               "Which day?",
		"What's the weather going to be tomorrow?": "Tomorrow: clear, a high of 12 and a low of 4, with a 10 percent chance of rain.",
		"How's the weather looking?":               "It's 7 degrees and cloudy. Today: a high of 9 and a low of 3, with a 60 percent chance of rain.",
		"How cold is it outside?":                  "It's 7 degrees and cloudy. Today: a high of 9 and a low of 3, with a 60 percent chance of rain.",
		"What's the weather on Saturday?":          "On Saturday: a high of 6 and a low of 1.",
		"Will it rain on Saturday?":                "Sorry, I don't know the chance of rain on Saturday.",
	} {
		if got := respond(t, f, "", transcript); got != want {
			t.Errorf("%q:\n got %q\nwant %q", transcript, got, want)
		}
	}
	if got := respond(t, f, "Copenhagen", "What's the weather?"); got != "In Copenhagen, it's 7 degrees and cloudy. Today: a high of 9 and a low of 3, with a 60 percent chance of rain." {
		t.Errorf("with place: %q", got)
	}
	if got := respond(t, f, "Copenhagen", "Will it rain tomorrow?"); got != "Probably not: a 10 percent chance of rain tomorrow in Copenhagen." {
		t.Errorf("rain with place: %q", got)
	}
	down := fakeForecaster{err: errors.WithMessage(fault.ErrServiceUnavailable, "offline")}
	if got := respond(t, down, "", "What's the weather?"); got != "Sorry, I can't reach that right now." {
		t.Errorf("offline: %q", got)
	}
}
