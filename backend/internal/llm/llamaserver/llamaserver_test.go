package llamaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

func TestChatJSON(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"intent\":\"time\",\"slots\":{}}"}}]}`)
	}))
	defer srv.Close()

	schema := map[string]any{"type": "object"}
	out, err := NewClient(srv.URL).ChatJSON(context.Background(),
		[]Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "what's the clock"}},
		schema, Options{Temperature: 0, MaxTokens: 64})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"intent":"time","slots":{}}` {
		t.Errorf("content = %s", out)
	}
	if got.ResponseFormat.Type != "json_schema" || !got.ResponseFormat.JSONSchema.Strict || !got.CachePrompt ||
		got.MaxTokens != 64 || len(got.Messages) != 2 || got.Messages[1].Content != "what's the clock" {
		t.Errorf("request = %+v", got)
	}
}

func TestChatJSONErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":{"message":"Loading model"}}`)
	}))
	defer srv.Close()
	if _, err := NewClient(srv.URL).ChatJSON(context.Background(), nil, nil, Options{}); !errors.Is(err, fault.ErrTemporary) {
		t.Errorf("503: error = %v, want fault.ErrTemporary", err)
	}

	gone := httptest.NewServer(http.NotFoundHandler())
	url := gone.URL
	gone.Close()
	if _, err := NewClient(url).ChatJSON(context.Background(), nil, nil, Options{}); !errors.Is(err, fault.ErrServiceUnavailable) {
		t.Errorf("connection refused: error = %v, want fault.ErrServiceUnavailable", err)
	}

	loading := NewClient(srv.URL)
	loading.ready.Store(false)
	if _, err := loading.ChatJSON(context.Background(), nil, nil, Options{}); !errors.Is(err, fault.ErrServiceUnavailable) {
		t.Errorf("not ready: error = %v, want fault.ErrServiceUnavailable", err)
	}
}

func TestStartMissingFiles(t *testing.T) {
	_, err := Start(Config{Binary: filepath.Join(t.TempDir(), "llama-server"), ModelFile: "model.gguf"})
	if !errors.Is(err, fault.ErrNotFound) {
		t.Errorf("error = %v, want fault.ErrNotFound", err)
	}
}
