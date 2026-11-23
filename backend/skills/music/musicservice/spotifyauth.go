package musicservice

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/tozd/go/errors"
	"golang.org/x/oauth2"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

// Spotify login: Authorization Code with PKCE (no client secret), once,
// through cmd/spotify-login; Alfred then refreshes the stored token.
const (
	spotifyAuthURL  = "https://accounts.spotify.com/authorize"
	spotifyTokenURL = "https://accounts.spotify.com/api/token"

	// SpotifyRedirectURL must be registered in the Spotify app. Spotify
	// accepts only HTTPS or loopback IP literals (not "localhost").
	SpotifyRedirectURL = "http://127.0.0.1:8888/callback"
)

// SpotifyScopes are what the music skill needs: reading and controlling
// playback on the user's Spotify Connect devices.
var SpotifyScopes = []string{"user-read-playback-state", "user-modify-playback-state"}

// SpotifyOAuth returns the OAuth configuration for the Spotify app with
// clientID. It is a public client (PKCE), so the client ID goes in the
// request parameters and there is no secret.
func SpotifyOAuth(clientID string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: clientID,
		Endpoint: oauth2.Endpoint{
			AuthURL:   spotifyAuthURL,
			TokenURL:  spotifyTokenURL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
		RedirectURL: SpotifyRedirectURL,
		Scopes:      SpotifyScopes,
	}
}

// DefaultSpotifyTokenFile is where the token is stored unless configured
// otherwise: ~/.config/alfred/spotify-token.json.
func DefaultSpotifyTokenFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "alfred", "spotify-token.json")
}

// LoadToken reads a token saved by SaveToken. A missing file is
// fault.ErrUnauthorized: nobody has logged in yet.
func LoadToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.WithMessagef(fault.ErrUnauthorized, "no Spotify login at %s (run make spotify-login)", path)
	}
	if err != nil {
		return nil, errors.WithMessage(err, "read Spotify token")
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, errors.Prefix(errors.WithMessagef(err, "Spotify token %s", path), fault.ErrDecoding)
	}
	if tok.RefreshToken == "" {
		return nil, errors.WithMessagef(fault.ErrUnauthorized, "Spotify token %s has no refresh token (run make spotify-login)", path)
	}
	return &tok, nil
}

// SaveToken writes tok to path, readable only by the user (it grants
// control of their Spotify account).
func SaveToken(path string, tok *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errors.WithMessage(err, "create token directory")
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return errors.WithStack(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return errors.WithMessage(err, "write Spotify token")
	}
	return nil
}

// savingTokenSource refreshes through src and saves the token whenever it
// changes: Spotify may rotate the refresh token, and the old one stops
// working.
type savingTokenSource struct {
	src  oauth2.TokenSource
	path string

	mu   sync.Mutex
	last oauth2.Token
}

func newSavingTokenSource(cfg *oauth2.Config, tok *oauth2.Token, path string) *savingTokenSource {
	// The context only carries the HTTP client for refresh requests (the
	// default client here); it is not a request deadline.
	return &savingTokenSource{
		src:  cfg.TokenSource(context.Background(), tok),
		path: path,
		last: *tok,
	}
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := s.src.Token()
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) {
		// invalid_grant: the refresh token was revoked or expired.
		return nil, errors.Prefix(errors.WithMessage(err, "refresh Spotify token"), fault.ErrUnauthorized)
	}
	if err != nil {
		return nil, errors.Prefix(errors.WithMessage(err, "refresh Spotify token"), fault.ErrServiceUnavailable)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if tok.AccessToken != s.last.AccessToken || tok.RefreshToken != s.last.RefreshToken {
		s.last = *tok
		if err := SaveToken(s.path, tok); err != nil {
			return nil, err
		}
	}
	return tok, nil
}
