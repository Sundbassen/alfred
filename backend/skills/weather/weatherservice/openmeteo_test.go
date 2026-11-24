package weatherservice

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

const sample = `{
  "timezone": "Europe/Copenhagen",
  "current": {"time": "2026-11-24T10:00", "temperature_2m": 7.3, "weather_code": 3},
  "daily": {
    "time": ["2026-11-24", "2026-11-25"],
    "weather_code": [63, 0],
    "temperature_2m_max": [9.4, 12.0],
    "temperature_2m_min": [2.6, 4.0],
    "precipitation_probability_max": [60, 10]
  }
}`

func TestForecast(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Get("latitude") != "55.6761" || q.Get("longitude") != "12.5683" || q.Get("timezone") != "auto" || q.Get("forecast_days") != "7" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, sample)
	}))
	defer srv.Close()
	o := NewOpenMeteo(OpenMeteoConfig{Latitude: 55.6761, Longitude: 12.5683})
	o.api = srv.URL

	f, err := o.Forecast(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.Now.Temperature != 7.3 || f.Now.Condition != "cloudy" || len(f.Days) != 2 {
		t.Fatalf("forecast = %+v", f)
	}
	d := f.Days[0]
	if d.Date.Format("2006-01-02 MST") != "2026-11-24 CET" || d.High != 9.4 || d.Low != 2.6 || d.RainChance != 60 || d.Condition != "rainy" {
		t.Errorf("day = %+v", d)
	}
	if _, err := o.Forecast(context.Background()); err != nil || calls != 1 {
		t.Errorf("second call: %v, %d API calls; want the cached forecast", err, calls)
	}
}

func TestForecastErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	o := NewOpenMeteo(OpenMeteoConfig{})
	o.api = srv.URL
	if _, err := o.Forecast(context.Background()); !errors.Is(err, fault.ErrTemporary) {
		t.Errorf("503: %v", err)
	}
	srv.Close()
	if _, err := o.Forecast(context.Background()); !errors.Is(err, fault.ErrServiceUnavailable) {
		t.Errorf("unreachable: %v", err)
	}
}
