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
	// Place names the location in answers ("In Copenhagen, …"); optional.
	Place string
}

// cacheFor is how long a forecast is reused before asking again.
const cacheFor = 10 * time.Minute

// OpenMeteo gets forecasts from the Open-Meteo API.
type OpenMeteo struct {
	cfg    OpenMeteoConfig
	api    string
	client *http.Client

	mu      sync.Mutex
	cached  weather.Forecast
	fetched time.Time
}

// NewOpenMeteo returns a forecaster for the configured location.
func NewOpenMeteo(cfg OpenMeteoConfig) *OpenMeteo {
	return &OpenMeteo{cfg: cfg, api: "https://api.open-meteo.com/v1/forecast", client: &http.Client{Timeout: 10 * time.Second}}
}

// Forecast returns the current weather and 7 days of forecast, cached for
// a few minutes.
func (o *OpenMeteo) Forecast(ctx context.Context) (weather.Forecast, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.fetched.IsZero() && time.Since(o.fetched) < cacheFor {
		return o.cached, nil
	}
	f, err := o.fetch(ctx)
	if err != nil {
		return weather.Forecast{}, err
	}
	o.cached, o.fetched = f, time.Now()
	return f, nil
}

type response struct {
	Timezone string `json:"timezone"`
	Current  struct {
		Temperature float64 `json:"temperature_2m"`
		WeatherCode int     `json:"weather_code"`
	} `json:"current"`
	Daily struct {
		Time        []string  `json:"time"`
		WeatherCode []int     `json:"weather_code"`
		Max         []float64 `json:"temperature_2m_max"`
		Min         []float64 `json:"temperature_2m_min"`
		RainChance  []int     `json:"precipitation_probability_max"`
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
		return weather.Forecast{}, errors.WithMessagef(category, "Open-Meteo: %d %s", resp.StatusCode, data)
	}
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return weather.Forecast{}, errors.Prefix(errors.WithMessage(err, "Open-Meteo response"), fault.ErrDecoding)
	}
	return r.forecast()
}

func (r response) forecast() (weather.Forecast, error) {
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil {
		loc = time.Local
	}
	d := r.Daily
	n := len(d.Time)
	if len(d.WeatherCode) != n || len(d.Max) != n || len(d.Min) != n || len(d.RainChance) != n {
		return weather.Forecast{}, errors.WithMessage(fault.ErrDecoding, "Open-Meteo daily arrays differ in length")
	}
	f := weather.Forecast{Now: weather.Current{Temperature: r.Current.Temperature, Condition: condition(r.Current.WeatherCode)}}
	for i := range n {
		date, err := time.ParseInLocation(time.DateOnly, d.Time[i], loc)
		if err != nil {
			return weather.Forecast{}, errors.Prefix(errors.WithMessagef(err, "Open-Meteo date %q", d.Time[i]), fault.ErrDecoding)
		}
		f.Days = append(f.Days, weather.Day{
			Date: date, High: d.Max[i], Low: d.Min[i], RainChance: d.RainChance[i], Condition: condition(d.WeatherCode[i]),
		})
	}
	return f, nil
}

// condition says a WMO weather code (as Open-Meteo reports it) in words.
func condition(code int) string {
	switch code {
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
