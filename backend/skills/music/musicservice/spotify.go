// Package musicservice holds the music skill's providers: implementations
// of music.Player. Spotify controls Spotify Connect devices through the
// Spotify Web API.
package musicservice

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gitlab.com/tozd/go/errors"
	"golang.org/x/oauth2"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/skills/music"
)

const spotifyAPI = "https://api.spotify.com/v1"

// Mix sizing: search returns at most 10 results per call (Spotify's
// February 2026 limit), so tracks per artist come from a few pages.
const (
	mixPagesPerArtist = 2
	mixPageSize       = 10
	mixMaxTracks      = 50
	maxRetryAfter     = 5 * time.Second
)

// SpotifyConfig configures the Spotify provider.
type SpotifyConfig struct {
	// ClientID of the user's Spotify app (developer.spotify.com, Web API,
	// redirect SpotifyRedirectURL). The app owner needs Premium.
	ClientID string
	// TokenFile holds the login written by cmd/spotify-login.
	TokenFile string
	// Device is the Spotify Connect name of this speaker's receiver (e.g.
	// spotifyd running with device_name "Alfred").
	Device string
}

// Spotify is a music.Player backed by the Spotify Web API.
type Spotify struct {
	client  *http.Client
	api     string
	device  string
	shuffle func(n int, swap func(i, j int))
}

var _ music.Player = (*Spotify)(nil)

// NewSpotify loads the stored login. Without one it returns
// fault.ErrUnauthorized ("run make spotify-login").
func NewSpotify(cfg SpotifyConfig) (*Spotify, error) {
	if cfg.ClientID == "" {
		return nil, errors.WithMessage(fault.ErrInvalidValue, "Spotify client ID is empty")
	}
	tok, err := LoadToken(cfg.TokenFile)
	if err != nil {
		return nil, err
	}
	src := oauth2.ReuseTokenSource(tok, newSavingTokenSource(SpotifyOAuth(cfg.ClientID), tok, cfg.TokenFile))
	return newSpotify(oauth2.NewClient(context.Background(), src), spotifyAPI, cfg.Device), nil
}

func newSpotify(client *http.Client, api, device string) *Spotify {
	client.Timeout = 10 * time.Second
	return &Spotify{client: client, api: api, device: device, shuffle: rand.Shuffle}
}

// Play searches for query and plays the best match on the target: an
// artist or playlist whose name matches the query exactly, otherwise the
// top track.
func (s *Spotify) Play(ctx context.Context, t music.Target, query string) (music.Item, error) {
	var res searchResult
	err := s.call(ctx, http.MethodGet, "/search", url.Values{
		"q": {query}, "type": {"track,artist,playlist"}, "limit": {"5"},
	}, nil, &res)
	if err != nil {
		return music.Item{}, err
	}

	var body playBody
	var item music.Item
	switch {
	case res.Artists.exact(query) != nil:
		a := res.Artists.exact(query)
		body, item = playBody{ContextURI: a.URI}, music.Item{Title: a.Name}
	case res.Playlists.exact(query) != nil:
		p := res.Playlists.exact(query)
		body, item = playBody{ContextURI: p.URI}, music.Item{Title: p.Name}
	case len(res.Tracks.Items) > 0:
		tr := res.Tracks.Items[0]
		body, item = playBody{URIs: []string{tr.URI}}, music.Item{Title: tr.Name, Artist: tr.firstArtist()}
	default:
		return music.Item{}, errors.WithMessagef(fault.ErrNotFound, "Spotify search %q: no results", query)
	}
	return item, s.playerCall(ctx, t, http.MethodPut, "/me/player/play", body)
}

// PlayMix plays a mix of the artists' tracks: each artist's tracks are
// shuffled, then interleaved, up to mixMaxTracks.
func (s *Spotify) PlayMix(ctx context.Context, t music.Target, artists []string) ([]string, error) {
	var names []string
	var perArtist [][]string
	for _, name := range artists {
		artist, err := s.findArtist(ctx, name)
		if err != nil {
			return nil, err
		}
		uris, err := s.artistTracks(ctx, artist)
		if err != nil {
			return nil, err
		}
		s.shuffle(len(uris), func(i, j int) { uris[i], uris[j] = uris[j], uris[i] })
		names = append(names, artist.Name)
		perArtist = append(perArtist, uris)
	}

	var mix []string
	for i := 0; len(mix) < mixMaxTracks; i++ {
		added := false
		for _, uris := range perArtist {
			if i < len(uris) && len(mix) < mixMaxTracks {
				mix = append(mix, uris[i])
				added = true
			}
		}
		if !added {
			break
		}
	}
	if len(mix) == 0 {
		return nil, errors.WithMessagef(fault.ErrNotFound, "no tracks for %v", artists)
	}
	return names, s.playerCall(ctx, t, http.MethodPut, "/me/player/play", playBody{URIs: mix})
}

func (s *Spotify) findArtist(ctx context.Context, name string) (artist, error) {
	var res searchResult
	err := s.call(ctx, http.MethodGet, "/search", url.Values{"q": {name}, "type": {"artist"}, "limit": {"1"}}, nil, &res)
	if err != nil {
		return artist{}, err
	}
	if len(res.Artists.Items) == 0 || res.Artists.Items[0] == nil {
		return artist{}, errors.WithMessagef(fault.ErrNotFound, "Spotify artist %q", name)
	}
	return *res.Artists.Items[0], nil
}

// artistTracks returns the URIs of tracks by a, from a few search pages.
// Search is fuzzy, so only tracks that list a as an artist are kept.
func (s *Spotify) artistTracks(ctx context.Context, a artist) ([]string, error) {
	seen := make(map[string]bool)
	var uris []string
	for page := range mixPagesPerArtist {
		var res searchResult
		err := s.call(ctx, http.MethodGet, "/search", url.Values{
			"q": {`artist:"` + a.Name + `"`}, "type": {"track"},
			"limit": {strconv.Itoa(mixPageSize)}, "offset": {strconv.Itoa(page * mixPageSize)},
		}, nil, &res)
		if err != nil {
			return nil, err
		}
		for _, tr := range res.Tracks.Items {
			if tr != nil && tr.by(a.ID) && !seen[tr.URI] {
				seen[tr.URI] = true
				uris = append(uris, tr.URI)
			}
		}
		if len(res.Tracks.Items) < mixPageSize {
			break
		}
	}
	return uris, nil
}

func (s *Spotify) Pause(ctx context.Context, t music.Target) error {
	return s.playerCall(ctx, t, http.MethodPut, "/me/player/pause", nil)
}

func (s *Spotify) Resume(ctx context.Context, t music.Target) error {
	return s.playerCall(ctx, t, http.MethodPut, "/me/player/play", nil)
}

func (s *Spotify) Next(ctx context.Context, t music.Target) error {
	return s.playerCall(ctx, t, http.MethodPost, "/me/player/next", nil)
}

func (s *Spotify) Previous(ctx context.Context, t music.Target) error {
	return s.playerCall(ctx, t, http.MethodPost, "/me/player/previous", nil)
}

// NowPlaying returns the track (or episode) playing on Spotify;
// fault.ErrNotFound when nothing is.
func (s *Spotify) NowPlaying(ctx context.Context, _ music.Target) (music.Item, error) {
	var res struct {
		IsPlaying bool   `json:"is_playing"`
		Item      *track `json:"item"`
	}
	if err := s.call(ctx, http.MethodGet, "/me/player/currently-playing", nil, nil, &res); err != nil {
		return music.Item{}, err
	}
	if !res.IsPlaying || res.Item == nil {
		return music.Item{}, errors.WithMessage(fault.ErrNotFound, "nothing is playing")
	}
	return music.Item{Title: res.Item.Name, Artist: res.Item.firstArtist()}, nil
}

// SetShuffle turns shuffle on or off on the target's device.
func (s *Spotify) SetShuffle(ctx context.Context, t music.Target, on bool) error {
	id, err := s.deviceID(ctx, t)
	if err != nil {
		return err
	}
	return s.call(ctx, http.MethodPut, "/me/player/shuffle",
		url.Values{"device_id": {id}, "state": {strconv.FormatBool(on)}}, nil, nil)
}

// playerCall runs a player command on the target's device.
func (s *Spotify) playerCall(ctx context.Context, t music.Target, method, path string, body any) error {
	id, err := s.deviceID(ctx, t)
	if err != nil {
		return err
	}
	return s.call(ctx, method, path, url.Values{"device_id": {id}}, body, nil)
}

// deviceID finds the Spotify Connect device to play on: the configured
// device for this speaker, or the target's speaker name.
func (s *Spotify) deviceID(ctx context.Context, t music.Target) (string, error) {
	name := s.device
	if t.Speaker != "" {
		name = t.Speaker
	}
	var res struct {
		Devices []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"devices"`
	}
	if err := s.call(ctx, http.MethodGet, "/me/player/devices", nil, nil, &res); err != nil {
		return "", err
	}
	for _, d := range res.Devices {
		if strings.EqualFold(d.Name, name) && d.ID != "" {
			return d.ID, nil
		}
	}
	return "", errors.WithMessagef(fault.ErrServiceUnavailable,
		"Spotify device %q is not available (is its Spotify Connect receiver running?)", name)
}

// call sends one Web API request and decodes the JSON response into out
// (if non-nil). Failures are fault categories; a 429 with a short
// Retry-After is retried once.
func (s *Spotify) call(ctx context.Context, method, path string, query url.Values, body, out any) error {
	for attempt := 0; ; attempt++ {
		resp, err := s.send(ctx, method, path, query, body)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return errors.Prefix(errors.WithMessagef(readErr, "Spotify %s %s", method, path), fault.ErrServiceUnavailable)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if wait, ok := retryAfter(resp); ok {
				select {
				case <-time.After(wait):
					continue
				case <-ctx.Done():
					return errors.WithStack(ctx.Err())
				}
			}
		}
		if category := fault.ErrorFromHTTPCode(resp.StatusCode); category != nil {
			return errors.WithMessagef(category, "Spotify %s %s: %d %s", method, path, resp.StatusCode, apiMessage(data))
		}
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.Prefix(errors.WithMessagef(err, "Spotify %s %s", method, path), fault.ErrDecoding)
		}
		return nil
	}
}

func (s *Spotify) send(ctx context.Context, method, path string, query url.Values, body any) (*http.Response, error) {
	u := s.api + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, errors.WithStack(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		// Token refresh errors already carry a category.
		if errors.Is(err, fault.ErrUnauthorized) || errors.Is(err, fault.ErrServiceUnavailable) {
			return nil, err
		}
		return nil, errors.Prefix(errors.WithMessagef(err, "Spotify %s %s", method, path), fault.ErrServiceUnavailable)
	}
	return resp, nil
}

func retryAfter(resp *http.Response) (time.Duration, bool) {
	secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || secs < 0 {
		return 0, false
	}
	wait := time.Duration(secs) * time.Second
	return wait, wait <= maxRetryAfter
}

// apiMessage extracts the message of a Web API error response.
func apiMessage(data []byte) string {
	var res struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &res) == nil && res.Error.Message != "" {
		return res.Error.Message
	}
	return strings.TrimSpace(string(data))
}

type playBody struct {
	ContextURI string   `json:"context_uri,omitempty"`
	URIs       []string `json:"uris,omitempty"`
}

// Search response; items may be null for unavailable entries.
type searchResult struct {
	Tracks    page[track]    `json:"tracks"`
	Artists   page[artist]   `json:"artists"`
	Playlists page[playlist] `json:"playlists"`
}

type page[T any] struct {
	Items []*T `json:"items"`
}

type artist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URI  string `json:"uri"`
}

type track struct {
	Name    string   `json:"name"`
	URI     string   `json:"uri"`
	Artists []artist `json:"artists"`
}

func (t *track) firstArtist() string {
	if len(t.Artists) == 0 {
		return ""
	}
	return t.Artists[0].Name
}

func (t *track) by(artistID string) bool {
	for _, a := range t.Artists {
		if a.ID == artistID {
			return true
		}
	}
	return false
}

type playlist struct {
	Name string `json:"name"`
	URI  string `json:"uri"`
}

func (p page[T]) exact(query string) *T {
	for _, item := range p.Items {
		if item != nil && strings.EqualFold(nameOf(item), strings.TrimSpace(query)) {
			return item
		}
	}
	return nil
}

func nameOf(v any) string {
	switch v := v.(type) {
	case *artist:
		return v.Name
	case *playlist:
		return v.Name
	case *track:
		return v.Name
	}
	return ""
}
