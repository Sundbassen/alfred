// Package weather is the weather skill: the current weather and the
// forecast for the coming week ("what's the weather tomorrow", "will it
// rain on friday"). The Forecaster interface is implemented by
// weatherservice (Open-Meteo) for the configured location.
package weather

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/spoken"
)

// Forecast is the weather now and for the coming days. Temperatures are in
// degrees Celsius.
type Forecast struct {
	Now  Current
	Days []Day // today first
}

// Current is the weather right now.
type Current struct {
	Temperature float64
	Condition   string // in words: "cloudy", "light rain"
}

// Day is one day's forecast.
type Day struct {
	Date       time.Time // midnight, local time
	High, Low  float64
	RainChance int // percent
	Condition  string
}

// Forecaster gets the forecast for this speaker's location.
type Forecaster interface {
	Forecast(ctx context.Context) (Forecast, error)
}

// Weather is "what's the weather (tomorrow)".
type Weather struct {
	Day string `slot:"day,optional"`
}

func (Weather) IntentName() string { return "weather" }

// Rain is "will it rain (on friday)", "do I need an umbrella".
type Rain struct {
	Day string `slot:"day,optional"`
}

func (Rain) IntentName() string { return "weather_rain" }

// Register adds the weather intents to r. place, if set, is said in the
// answers ("In Copenhagen, it's …").
func Register(r *assistant.Router, f Forecaster, place string) {
	register(r, f, place, time.Now)
}

func register(r *assistant.Router, f Forecaster, place string, now func() time.Time) {
	h := &handler{forecaster: f, place: place, now: now}
	assistant.Handle(r, assistant.Spec{
		Description: "Tell the weather now or the forecast for a day in the coming week",
		Phrases: []string{
			"what is the weather", "what is the weather like", "what is the weather {day}",
			"what is the weather like {day}", "how is the weather", "how is the weather {day}",
			"what is the forecast", "what is the forecast for {day}", "how cold is it", "how warm is it",
			"what is the temperature",
		},
		Help: "tell you the weather",
	}, h.weather)
	assistant.Handle(r, assistant.Spec{
		Description: "Say whether it will rain on a day in the coming week",
		Phrases: []string{
			"will it rain", "will it rain {day}", "is it going to rain", "is it going to rain {day}",
			"do i need an umbrella", "do i need an umbrella {day}",
		},
	}, h.rain)
}

type handler struct {
	forecaster Forecaster
	place      string
	now        func() time.Time
}

// day finds the forecast for the spoken day ("" is today).
func (h *handler) day(ctx context.Context, said string) (Forecast, Day, string, error) {
	now := h.now()
	date, ok := spoken.ParseDay(match.Normalize(said), now)
	if !ok {
		return Forecast{}, Day{}, "", &assistant.SpokenError{Text: "Sorry, which day?",
			Err: errors.WithMessagef(fault.ErrInvalidValue, "day %q", said)}
	}
	f, err := h.forecaster.Forecast(ctx)
	if err != nil {
		return Forecast{}, Day{}, "", err
	}
	for _, d := range f.Days {
		if sameDay(d.Date, date) {
			return f, d, dayName(date, now), nil
		}
	}
	return Forecast{}, Day{}, "", &assistant.SpokenError{Text: "Sorry, I only have the forecast for the coming week.",
		Err: errors.WithMessagef(fault.ErrNotFound, "no forecast for %s", date.Format(time.DateOnly))}
}

func (h *handler) weather(ctx context.Context, in Weather) (assistant.Response, error) {
	f, d, name, err := h.day(ctx, in.Day)
	if err != nil {
		return assistant.Response{}, err
	}
	summary := fmt.Sprintf("a high of %s and a low of %s", degrees(d.High), degrees(d.Low))
	if d.RainChance > 0 {
		summary += fmt.Sprintf(", with a %d percent chance of rain", d.RainChance)
	}
	var text string
	if name == "today" {
		text = fmt.Sprintf("%sit's %s degrees and %s. Today: %s.", h.in(), degrees(f.Now.Temperature), f.Now.Condition, summary)
	} else {
		text = fmt.Sprintf("%s%s: %s, %s.", h.in(), name, d.Condition, summary)
	}
	return assistant.Response{Text: capitalize(text)}, nil
}

func (h *handler) rain(ctx context.Context, in Rain) (assistant.Response, error) {
	_, d, name, err := h.day(ctx, in.Day)
	if err != nil {
		return assistant.Response{}, err
	}
	chance := fmt.Sprintf("a %d percent chance of rain %s", d.RainChance, strings.ToLower(name))
	switch {
	case d.RainChance >= 50:
		return assistant.Response{Text: "Yes, " + chance + "."}, nil
	case d.RainChance >= 20:
		return assistant.Response{Text: "Maybe: " + chance + "."}, nil
	}
	return assistant.Response{Text: "Probably not: " + chance + "."}, nil
}

func (h *handler) in() string {
	if h.place == "" {
		return ""
	}
	return "In " + h.place + ", "
}

// dayName says a day: "today", "tomorrow", "on Friday".
func dayName(date, now time.Time) string {
	switch {
	case sameDay(date, now):
		return "today"
	case sameDay(date, now.AddDate(0, 0, 1)):
		return "tomorrow"
	}
	return "on " + date.Weekday().String()
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// degrees says a temperature, rounded: "7", "minus 3".
func degrees(t float64) string {
	n := int(math.Round(t))
	if n < 0 {
		return fmt.Sprintf("minus %d", -n)
	}
	return fmt.Sprint(n)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
