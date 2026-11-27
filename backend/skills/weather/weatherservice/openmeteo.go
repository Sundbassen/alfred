// Package weatherservice implements weather.Forecaster with Open-Meteo
// (open-meteo.com: free, no API key; data CC BY 4.0).
package weatherservice

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/skills/weather"
)

// OpenMeteoConfig is the speaker's location.
type OpenMeteoConfig struct {
	Latitude, Longitude float64
}

// cacheFor is how long a forecast is reused before asking again.
const cacheFor = 10 * time.Minute

// OpenMeteo gets forecasts from the Open-Meteo API.
type OpenMeteo struct {
	cfg    OpenMeteoConfig
	api    string
	client *http.Client

	now     func() time.Time
	mu      sync.Mutex
	cached  weather.Forecast
	fetched time.Time
}

// NewOpenMeteo returns a forecaster for the configured location.
func NewOpenMeteo(cfg OpenMeteoConfig) *OpenMeteo {
	return &OpenMeteo{cfg: cfg, api: "https://api.open-meteo.com/v1/forecast",
		client: &http.Client{Timeout: 10 * time.Second}, now: time.Now}
}

// Forecast returns the current weather and 7 days of forecast, cached for
// a few minutes.
func (o *OpenMeteo) Forecast(ctx context.Context) (weather.Forecast, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.fetched.IsZero() && o.now().Sub(o.fetched) < cacheFor {
		return o.cached, nil
	}
	f, err := o.fetch(ctx)
	if err != nil {
		return weather.Forecast{}, err
	}
	o.cached, o.fetched = f, o.now()
	return f, nil
}

// response is Open-Meteo's answer. Values are pointers: any of them can be
// null where a weather model has no data.
type response struct {
	Timezone         string `json:"timezone"`
	UTCOffsetSeconds int    `json:"utc_offset_seconds"`
	Current          struct {
		Temperature *float64 `json:"temperature_2m"`
		WeatherCode *int     `json:"weather_code"`
	} `json:"current"`
	Daily struct {
		Time        []string   `json:"time"`
		WeatherCode []*int     `json:"weather_code"`
		Max         []*float64 `json:"temperature_2m_max"`
		Min         []*float64 `json:"temperature_2m_min"`
		RainChance  []*int     `json:"precipitation_probability_max"`
	} `json:"daily"`
}

func (o *OpenMeteo) fetch(ctx context.Context) (weather.Forecast, error) {
	q := url.Values{
		"latitude":      {strconv.FormatFloat(o.cfg.Latitude, 'f', 4, 64)},
		"longitude":     {strconv.FormatFloat(o.cfg.Longitude, 'f', 4, 64)},
		"current":       {"temperature_2m,weather_code"},
		"daily":         {"weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max"},
		"timezone":      {"auto"},
		"forecast_days": {"7"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.api+"?"+q.Encode(), nil)
	if err != nil {
		return weather.Forecast{}, errors.WithStack(err)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return weather.Forecast{}, errors.Prefix(errors.WithMessage(err, "Open-Meteo"), fault.ErrServiceUnavailable)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return weather.Forecast{}, errors.Prefix(errors.WithMessage(err, "read Open-Meteo response"), fault.ErrServiceUnavailable)
	}
	if category := fault.ErrorFromHTTPCode(resp.StatusCode); category != nil {
		return weather.Forecast{}, errors.WithMessagef(category, "Open-Meteo: %d %s", resp.StatusCode, reason(data))
	}
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return weather.Forecast{}, errors.Prefix(errors.WithMessage(err, "Open-Meteo response"), fault.ErrDecoding)
	}
	return r.forecast()
}

// reason is what to log about an error response: Open-Meteo's reason
// ({"error": true, "reason": "..."}), or the start of the body.
func reason(data []byte) string {
	var e struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(data, &e) == nil && e.Reason != "" {
		return e.Reason
	}
	if len(data) > 200 {
		return string(data[:200]) + "…"
	}
	return string(data)
}

func (r response) forecast() (weather.Forecast, error) {
	// Only the calendar dates matter, so the zone at the current offset is
	// enough (and needs no time zone database).
	loc := time.FixedZone(r.Timezone, r.UTCOffsetSeconds)
	d := r.Daily
	n := len(d.Time)
	if len(d.WeatherCode) != n || len(d.Max) != n || len(d.Min) != n || len(d.RainChance) != n {
		return weather.Forecast{}, errors.WithMessage(fault.ErrDecoding, "Open-Meteo daily arrays differ in length")
	}
	if r.Current.Temperature == nil {
		return weather.Forecast{}, errors.WithMessage(fault.ErrDecoding, "Open-Meteo has no current temperature")
	}
	f := weather.Forecast{Now: weather.Current{Temperature: *r.Current.Temperature, Condition: condition(r.Current.WeatherCode)}}
	for i := range n {
		if d.Max[i] == nil || d.Min[i] == nil {
			continue // no forecast for that day
		}
		date, err := time.ParseInLocation(time.DateOnly, d.Time[i], loc)
		if err != nil {
			return weather.Forecast{}, errors.Prefix(errors.WithMessagef(err, "Open-Meteo date %q", d.Time[i]), fault.ErrDecoding)
		}
		rain := -1
		if d.RainChance[i] != nil {
			rain = *d.RainChance[i]
		}
		f.Days = append(f.Days, weather.Day{
			Date: date, High: *d.Max[i], Low: *d.Min[i], RainChance: rain, Condition: condition(d.WeatherCode[i]),
		})
	}
	return f, nil
}

// condition says a WMO weather code (as Open-Meteo reports it) in words,
// "" when there is none.
func condition(c *int) string {
	if c == nil {
		return ""
	}
	switch *c {
	case 0:
		return "clear"
	case 1:
		return "mostly clear"
	case 2:
		return "partly cloudy"
	case 3:
		return "cloudy"
	case 45, 48:
		return "foggy"
	case 51, 53, 55, 56, 57:
		return "drizzly"
	case 61, 80:
		return "light rain"
	case 63, 81:
		return "rainy"
	case 65, 82:
		return "heavy rain"
	case 66, 67:
		return "freezing rain"
	case 71, 85:
		return "light snow"
	case 73, 75, 77, 86:
		return "snowy"
	case 95, 96, 99:
		return "thunderstorms"
	}
	return "mixed weather"
}
