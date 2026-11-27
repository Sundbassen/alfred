package weatherservice

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

const sample = `{
  "timezone": "Europe/Copenhagen",
  "utc_offset_seconds": 3600,
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
	if _, offset := d.Date.Zone(); d.Date.Format(time.DateOnly) != "2026-11-24" || offset != 3600 || d.High != 9.4 || d.Low != 2.6 || d.RainChance != 60 || d.Condition != "rainy" {
		t.Errorf("day = %+v", d)
	}
	if _, err := o.Forecast(context.Background()); err != nil || calls != 1 {
		t.Errorf("second call: %v, %d API calls; want the cached forecast", err, calls)
	}
	now := time.Now().Add(cacheFor + time.Second)
	o.now = func() time.Time { return now }
	if _, err := o.Forecast(context.Background()); err != nil || calls != 2 {
		t.Errorf("after the cache expired: %v, %d API calls; want a new one", err, calls)
	}
}

func TestForecastNulls(t *testing.T) {
	// Some weather models have no rain chance or no forecast for a day.
	const nulls = `{
	  "timezone": "GMT", "utc_offset_seconds": 0,
	  "current": {"temperature_2m": 7.3, "weather_code": null},
	  "daily": {
	    "time": ["2026-11-24", "2026-11-25"],
	    "weather_code": [63, null],
	    "temperature_2m_max": [9.4, null],
	    "temperature_2m_min": [2.6, null],
	    "precipitation_probability_max": [null, null]
	  }
	}`
	f, err := serve(t, http.StatusOK, nulls).Forecast(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.Now.Condition != "" || len(f.Days) != 1 || f.Days[0].RainChance != -1 {
		t.Errorf("forecast = %+v; want no condition now, one day, rain chance unknown", f)
	}
}

func serve(t *testing.T, status int, body string) *OpenMeteo {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	o := NewOpenMeteo(OpenMeteoConfig{})
	o.api = srv.URL
	return o
}

func TestForecastBadResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       error
		inMessage  string
	}{
		{"malformed body", "{", http.StatusOK, fault.ErrDecoding, ""},
		{"daily arrays differ", `{"current": {"temperature_2m": 1}, "daily": {"time": ["2026-11-24"], "weather_code": []}}`,
			http.StatusOK, fault.ErrDecoding, ""},
		{"no current temperature", `{"current": {}, "daily": {}}`, http.StatusOK, fault.ErrDecoding, ""},
		{"bad parameters", `{"error": true, "reason": "Latitude must be in range of -90 to 90°."}`,
			http.StatusBadRequest, fault.ErrInvalidValue, "Latitude must be in range"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := serve(t, tc.status, tc.body).Forecast(context.Background())
			if !errors.Is(err, tc.want) || (tc.inMessage != "" && !strings.Contains(err.Error(), tc.inMessage)) {
				t.Errorf("error = %v, want %v containing %q", err, tc.want, tc.inMessage)
			}
		})
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
