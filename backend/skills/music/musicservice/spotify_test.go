package musicservice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/tozd/go/errors"
	"golang.org/x/oauth2"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/skills/music"
)

// fakeAPI stands in for the Spotify Web API.
type fakeAPI struct {
	t       *testing.T
	mu      sync.Mutex
	devices string // JSON devices list
	search  func(q searchQuery) string
	plays   []string // recorded player requests: "METHOD path?device body"
	status  int      // if set, every response has this status
	current string   // currently-playing JSON ("" → 204 No Content)
}

type searchQuery struct{ q, typ, offset string }

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status != 0 {
		w.WriteHeader(f.status)
		fmt.Fprint(w, `{"error":{"status":`+fmt.Sprint(f.status)+`,"message":"nope"}}`)
		return
	}
	switch {
	case r.URL.Path == "/search":
		v := r.URL.Query()
		fmt.Fprint(w, f.search(searchQuery{q: v.Get("q"), typ: v.Get("type"), offset: v.Get("offset")}))
	case r.URL.Path == "/me/player/devices":
		fmt.Fprint(w, f.devices)
	case r.URL.Path == "/me/player/currently-playing":
		if f.current == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		fmt.Fprint(w, f.current)
	case strings.HasPrefix(r.URL.Path, "/me/player/"):
		body, _ := io.ReadAll(r.Body)
		query := r.URL.Query().Get("device_id")
		if state := r.URL.Query().Get("state"); state != "" {
			query += "&state=" + state
		}
		f.plays = append(f.plays, fmt.Sprintf("%s %s?%s %s", r.Method, r.URL.Path, query, body))
		w.WriteHeader(http.StatusNoContent)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusNotFound)
	}
}

const devices = `{"devices":[{"id":"phone1","name":"Pixel"},{"id":"alfred1","name":"Alfred"}]}`

func newTestSpotify(t *testing.T, api *fakeAPI) *Spotify {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	s := newSpotify(srv.Client(), srv.URL, "alfred")
	s.shuffle = func(int, func(i, j int)) {} // deterministic
	return s
}

func TestPlayTopTrack(t *testing.T) {
	api := &fakeAPI{t: t, devices: devices, search: func(searchQuery) string {
		return `{"tracks":{"items":[{"name":"Get Lucky","uri":"spotify:track:1","artists":[{"name":"Daft Punk","id":"dp"}]}]},
			"artists":{"items":[null,{"name":"Daft Punkers","uri":"spotify:artist:x","id":"x"}]},"playlists":{"items":[null]}}`
	}}
	item, err := newTestSpotify(t, api).Play(context.Background(), music.Target{}, "get lucky")
	if err != nil {
		t.Fatal(err)
	}
	if item != (music.Item{Title: "Get Lucky", Artist: "Daft Punk"}) {
		t.Errorf("item = %+v", item)
	}
	want := `PUT /me/player/play?alfred1 {"uris":["spotify:track:1"]}`
	if len(api.plays) != 1 || api.plays[0] != want {
		t.Errorf("plays = %q, want %q", api.plays, want)
	}
}

func TestPlayExactArtistAsContext(t *testing.T) {
	api := &fakeAPI{t: t, devices: devices, search: func(searchQuery) string {
		return `{"tracks":{"items":[{"name":"Around the World","uri":"spotify:track:2","artists":[{"name":"Daft Punk","id":"dp"}]}]},
			"artists":{"items":[{"name":"Daft Punk","uri":"spotify:artist:dp","id":"dp"}]}}`
	}}
	item, err := newTestSpotify(t, api).Play(context.Background(), music.Target{}, "daft punk")
	if err != nil {
		t.Fatal(err)
	}
	if item.Title != "Daft Punk" || item.Artist != "" {
		t.Errorf("item = %+v", item)
	}
	if want := `PUT /me/player/play?alfred1 {"context_uri":"spotify:artist:dp"}`; api.plays[0] != want {
		t.Errorf("play = %q, want %q", api.plays[0], want)
	}
}

func TestPlayNoResults(t *testing.T) {
	api := &fakeAPI{t: t, devices: devices, search: func(searchQuery) string { return `{"tracks":{"items":[]}}` }}
	_, err := newTestSpotify(t, api).Play(context.Background(), music.Target{}, "zzyzx")
	if !errors.Is(err, fault.ErrNotFound) || len(api.plays) != 0 {
		t.Errorf("error = %v, plays = %v; want ErrNotFound and no play", err, api.plays)
	}
}

func TestPlayMix(t *testing.T) {
	tracks := func(artistID string, n int, offset string) string {
		var items []string
		for i := range n {
			items = append(items, fmt.Sprintf(`{"name":"t","uri":"spotify:track:%s%s-%d","artists":[{"id":%q}]}`, artistID, offset, i, artistID))
		}
		// A fuzzy search result by someone else must be dropped.
		items = append(items, `{"name":"cover","uri":"spotify:track:cover","artists":[{"id":"other"}]}`)
		return `{"tracks":{"items":[` + strings.Join(items, ",") + `]}}`
	}
	api := &fakeAPI{t: t, devices: devices, search: func(u searchQuery) string {
		switch {
		case u.typ == "artist" && u.q == "daft punk":
			return `{"artists":{"items":[{"name":"Daft Punk","id":"dp","uri":"spotify:artist:dp"}]}}`
		case u.typ == "artist" && u.q == "justice":
			return `{"artists":{"items":[{"name":"Justice","id":"ju","uri":"spotify:artist:ju"}]}}`
		case u.q == `artist:"Daft Punk"`:
			return tracks("dp", 2, u.offset) // fewer than a page: no second page
		case u.q == `artist:"Justice"`:
			return tracks("ju", 2, u.offset)
		}
		t.Errorf("unexpected search %+v", u)
		return `{}`
	}}
	names, err := newTestSpotify(t, api).PlayMix(context.Background(), music.Target{}, []string{"daft punk", "justice"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "Daft Punk,Justice" {
		t.Errorf("names = %v", names)
	}
	var body playBody
	if _, err := fmt.Sscanf(api.plays[0], "PUT /me/player/play?alfred1 %s", new(string)); err != nil {
		t.Fatalf("play = %q", api.plays[0])
	}
	if err := json.Unmarshal([]byte(strings.SplitN(api.plays[0], " ", 3)[2]), &body); err != nil {
		t.Fatal(err)
	}
	want := "spotify:track:dp0-0 spotify:track:ju0-0 spotify:track:dp0-1 spotify:track:ju0-1"
	if got := strings.Join(body.URIs, " "); got != want {
		t.Errorf("mix = %s, want interleaved %s (and no cover)", got, want)
	}
}

func TestPlayMixUnknownArtist(t *testing.T) {
	api := &fakeAPI{t: t, devices: devices, search: func(searchQuery) string { return `{"artists":{"items":[]}}` }}
	_, err := newTestSpotify(t, api).PlayMix(context.Background(), music.Target{}, []string{"nobody"})
	if !errors.Is(err, fault.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestControlsTargetTheDevice(t *testing.T) {
	api := &fakeAPI{t: t, devices: devices}
	s := newTestSpotify(t, api)
	ctx := context.Background()
	for _, call := range []func() error{
		func() error { return s.Pause(ctx, music.Target{}) },
		func() error { return s.Resume(ctx, music.Target{}) },
		func() error { return s.Next(ctx, music.Target{}) },
		func() error { return s.Previous(ctx, music.Target{Speaker: "pixel"}) },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"PUT /me/player/pause?alfred1 ", "PUT /me/player/play?alfred1 ",
		"POST /me/player/next?alfred1 ", "POST /me/player/previous?phone1 ",
	}
	if strings.Join(api.plays, "|") != strings.Join(want, "|") {
		t.Errorf("plays = %q, want %q", api.plays, want)
	}
}

func TestDeviceNotAvailable(t *testing.T) {
	api := &fakeAPI{t: t, devices: `{"devices":[{"id":"phone1","name":"Pixel"}]}`}
	err := newTestSpotify(t, api).Pause(context.Background(), music.Target{})
	if !errors.Is(err, fault.ErrServiceUnavailable) {
		t.Errorf("error = %v, want ErrServiceUnavailable", err)
	}
}

func TestHTTPErrorsBecomeCategories(t *testing.T) {
	for status, want := range map[int]error{
		401: fault.ErrUnauthorized, 403: fault.ErrPermission, 404: fault.ErrNotFound, 502: fault.ErrTemporary,
	} {
		api := &fakeAPI{t: t, status: status}
		err := newTestSpotify(t, api).Pause(context.Background(), music.Target{})
		if !errors.Is(err, want) || !strings.Contains(err.Error(), "nope") {
			t.Errorf("status %d: error = %v, want %v with the API message", status, err, want)
		}
	}
}

func TestRefreshedTokenIsSaved(t *testing.T) {
	var refreshes int
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "old-refresh" || r.Form.Get("client_id") != "cid" {
			t.Errorf("refresh request form = %v", r.Form)
		}
		refreshes++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"new-access","token_type":"Bearer","expires_in":3600,"refresh_token":"new-refresh"}`)
	}))
	defer tokenSrv.Close()

	path := filepath.Join(t.TempDir(), "token.json")
	cfg := SpotifyOAuth("cid")
	cfg.Endpoint.TokenURL = tokenSrv.URL
	expired := &oauth2.Token{AccessToken: "old-access", RefreshToken: "old-refresh", Expiry: time.Now().Add(-time.Hour)}

	tok, err := newSavingTokenSource(cfg, expired, path).Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "new-access" || refreshes != 1 {
		t.Errorf("token = %+v after %d refreshes", tok, refreshes)
	}
	saved, err := LoadToken(path)
	if err != nil || saved.RefreshToken != "new-refresh" {
		t.Errorf("saved token = %+v, %v; want the rotated refresh token", saved, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

func TestRevokedRefreshIsUnauthorized(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Refresh token revoked"}`)
	}))
	defer tokenSrv.Close()
	cfg := SpotifyOAuth("cid")
	cfg.Endpoint.TokenURL = tokenSrv.URL
	expired := &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Now().Add(-time.Hour)}

	_, err := newSavingTokenSource(cfg, expired, filepath.Join(t.TempDir(), "t.json")).Token()
	if !errors.Is(err, fault.ErrUnauthorized) {
		t.Errorf("error = %v, want ErrUnauthorized", err)
	}
}

func TestNoLoginIsUnauthorized(t *testing.T) {
	_, err := NewSpotify(SpotifyConfig{ClientID: "cid", TokenFile: filepath.Join(t.TempDir(), "missing.json"), Device: "Alfred"})
	if !errors.Is(err, fault.ErrUnauthorized) {
		t.Errorf("error = %v, want ErrUnauthorized", err)
	}
}

func TestNowPlaying(t *testing.T) {
	api := &fakeAPI{t: t, devices: devices,
		current: `{"is_playing":true,"item":{"name":"One More Time","artists":[{"name":"Daft Punk"}]}}`}
	s := newTestSpotify(t, api)
	item, err := s.NowPlaying(context.Background(), music.Target{})
	if err != nil || item != (music.Item{Title: "One More Time", Artist: "Daft Punk"}) {
		t.Errorf("NowPlaying = %+v, %v", item, err)
	}
	api.current = ""
	if _, err := s.NowPlaying(context.Background(), music.Target{}); !errors.Is(err, fault.ErrNotFound) {
		t.Errorf("nothing playing: %v, want ErrNotFound", err)
	}
	api.current = `{"is_playing":false,"item":{"name":"One More Time"}}`
	if _, err := s.NowPlaying(context.Background(), music.Target{}); !errors.Is(err, fault.ErrNotFound) {
		t.Errorf("paused: %v, want ErrNotFound", err)
	}
}

func TestSetShuffle(t *testing.T) {
	api := &fakeAPI{t: t, devices: devices}
	s := newTestSpotify(t, api)
	if err := s.SetShuffle(context.Background(), music.Target{}, true); err != nil {
		t.Fatal(err)
	}
	if want := "PUT /me/player/shuffle?alfred1&state=true "; len(api.plays) != 1 || api.plays[0] != want {
		t.Errorf("plays = %q, want %q", api.plays, want)
	}
}
