// spotify-login logs Alfred in to Spotify once (Authorization Code with
// PKCE) and stores the token Alfred refreshes from then on.
//
//	ALFRED_SPOTIFY_CLIENT_ID=... go run ./cmd/spotify-login [-token-file path]
//
// Open the printed URL in a browser on this machine, approve, and the
// token is written (default ~/.config/alfred/spotify-token.json). The
// Spotify app must have the redirect URI http://127.0.0.1:8888/callback.
// On a headless box, run this on a machine with a browser and copy the
// token file over.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"gitlab.com/tozd/go/errors"
	"golang.org/x/oauth2"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/skills/music/musicservice"
)

func main() {
	clientID := flag.String("client-id", os.Getenv("ALFRED_SPOTIFY_CLIENT_ID"), "Spotify app client ID (default $ALFRED_SPOTIFY_CLIENT_ID)")
	tokenFile := flag.String("token-file", envOr("ALFRED_SPOTIFY_TOKEN_FILE", musicservice.DefaultSpotifyTokenFile()),
		"where to store the token (default $ALFRED_SPOTIFY_TOKEN_FILE or ~/.config/alfred/spotify-token.json)")
	flag.Parse()

	if *clientID == "" {
		fmt.Fprintln(os.Stderr, "missing client ID: set ALFRED_SPOTIFY_CLIENT_ID or pass -client-id")
		os.Exit(2)
	}
	if err := login(*clientID, *tokenFile); err != nil {
		fmt.Fprintln(os.Stderr, "spotify-login:", err)
		os.Exit(1)
	}
	fmt.Println("Logged in. Token saved to", *tokenFile)
}

func login(clientID, tokenFile string) error {
	cfg := musicservice.SpotifyOAuth(clientID)
	redirect, err := url.Parse(cfg.RedirectURL)
	if err != nil {
		return errors.WithStack(err)
	}
	verifier := oauth2.GenerateVerifier()
	state := randomState()

	listener, err := net.Listen("tcp", redirect.Host)
	if err != nil {
		return errors.WithMessagef(err, "listen on %s for the redirect", redirect.Host)
	}

	codes := make(chan string, 1)
	failures := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(redirect.Path, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("state") != state:
			http.Error(w, "state mismatch", http.StatusBadRequest)
			failures <- errors.WithMessage(fault.ErrInvalidValue, "state mismatch in redirect")
		case q.Get("error") != "":
			http.Error(w, "login refused: "+q.Get("error"), http.StatusBadRequest)
			failures <- errors.WithMessagef(fault.ErrPermission, "login refused: %s", q.Get("error"))
		default:
			fmt.Fprintln(w, "Alfred is logged in to Spotify. You can close this tab.")
			codes <- q.Get("code")
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go server.Serve(listener) //nolint:errcheck // stopped by Shutdown below
	defer server.Shutdown(context.Background())

	fmt.Println("Open this URL in a browser on this machine and approve:")
	fmt.Println()
	fmt.Println(cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)))
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var code string
	select {
	case code = <-codes:
	case err := <-failures:
		return err
	case <-ctx.Done():
		return errors.WithMessage(fault.ErrTimeout, "no login within 5 minutes")
	}

	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return errors.WithMessage(err, "exchange code for token")
	}
	return musicservice.SaveToken(tokenFile, tok)
}

func randomState() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
