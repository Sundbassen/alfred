package answer

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/llm/llamaserver"
	"github.com/sundbassen/alfred/internal/llm/ministral"
)

// TestWithTheRealModel asks the real local model known questions and logs
// each answer with its confidence: the data minMean and minLeast are tuned
// on. It needs the model (./download-model.sh --llm, make install) and runs
// only with ALFRED_TEST_MODEL=1 (it takes about a minute):
//
//	ALFRED_TEST_MODEL=1 make test
func TestWithTheRealModel(t *testing.T) {
	if os.Getenv("ALFRED_TEST_MODEL") == "" {
		t.Skip("set ALFRED_TEST_MODEL=1 to run against the real model")
	}
	modelFile := filepath.Join("..", "..", "models", "llm", ministral.ModelFile)
	binary := os.Getenv("ALFRED_LLAMA_SERVER")
	if !filepath.IsAbs(binary) {
		binary = filepath.Join("..", "..", binary)
	}
	server, err := llamaserver.Start(llamaserver.Config{Binary: binary, ModelFile: modelFile, Parallel: 2, ContextSize: 8192})
	if err != nil {
		t.Skipf("no model: %v", err)
	}
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := server.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}

	r := assistant.NewRouter()
	Register(r, ministral.New(server), slog.New(slog.NewTextHandler(testWriter{t}, nil)))
	ask := func(q string) string {
		start := time.Now()
		resp, err := r.Dispatch(context.Background(), Ask{Question: q})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		t.Logf("%.1fs %q → %q", time.Since(start).Seconds(), q, resp.Text)
		return resp.Text
	}

	// Numbers are computed by code, so these are exact.
	for _, tt := range [][2]string{
		{"What is 15 times 23?", "That's 345."},
		{"What's 20 percent of 80?", "That's 16."},
		{"What is the square root of 144?", "That's 12."},
		{"How many grams in a pound?", "1 pound is 453.59 grams."},
		{"What is 30 degrees Celsius in Fahrenheit?", "30 degrees Celsius is 86 degrees Fahrenheit."},
		{"How many feet is 3 meters?", "3 meters is 9.84 feet."},
	} {
		if got := ask(tt[0]); got != tt[1] {
			t.Errorf("%q: %q, want %q", tt[0], got, tt[1])
		}
	}
	// Well-known facts are answered.
	for _, tt := range [][2]string{
		{"Who wrote Hamlet?", "Shakespeare"},
		{"What is the capital of Australia?", "Canberra"},
		{"When was the Eiffel Tower built?", "1889"},
	} {
		if got := ask(tt[0]); !strings.Contains(got, tt[1]) {
			t.Errorf("%q: %q, want it to mention %s", tt[0], got, tt[1])
		}
	}
	// Obscure questions it made answers up for in tuning must not be said.
	for _, q := range []string{
		"Who was the mayor of Aarhus in 1923?",
		"What's the tallest mountain in Denmark?",
		"Who won the 1987 Danish chess championship?",
		"Tell me a joke",
	} {
		if got := ask(q); got != UnsureText && got != UnknownText {
			t.Errorf("%q: said %q, want it unsure", q, got)
		}
	}
}

// testWriter sends the skill's logs (with the confidences) to the test log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}
