// Package ministral is Mistral AI's Ministral 3 3B Instruct as Alfred's
// local intent model (an llm.Model), run by llama.cpp's llama-server.
// Another model is another package implementing llm.Model, chosen in
// cmd/internal/wiring.
package ministral

import (
	"context"

	"github.com/sundbassen/alfred/internal/assistant/llm"
	"github.com/sundbassen/alfred/internal/llm/llamaserver"
)

// ModelFile is the GGUF build download-model.sh --llm fetches
// (mistralai/Ministral-3-3B-Instruct-2512-GGUF, Apache-2.0).
const ModelFile = "Ministral-3-3B-Instruct-2512-Q4_K_M.gguf"

// Name identifies the model in recognitions and logs.
const Name = "ministral"

// Sampling: deterministic, and an intent answer is a short JSON object.
const (
	temperature = 0
	maxTokens   = 128
)

// ChatServer is what Ministral needs from a model server.
type ChatServer interface {
	ChatJSON(ctx context.Context, messages []llamaserver.Message, schema any, opts llamaserver.Options) (llamaserver.Completion, error)
}

// Ministral implements llm.Model.
type Ministral struct {
	server ChatServer
}

var _ llm.Model = (*Ministral)(nil)

// New returns the model served by server (a *llamaserver.Server running
// ModelFile).
func New(server ChatServer) *Ministral {
	return &Ministral{server: server}
}

// Generate asks Ministral for the JSON answer. Ministral 3's chat template
// supports a system message, which carries the static intent description
// (cached by llama-server across requests).
func (m *Ministral) Generate(ctx context.Context, req llm.Request) (llm.Result, error) {
	limit := maxTokens
	if req.MaxTokens > 0 {
		limit = req.MaxTokens
	}
	c, err := m.server.ChatJSON(ctx, []llamaserver.Message{
		{Role: "system", Content: req.System},
		{Role: "user", Content: req.User},
	}, req.Schema, llamaserver.Options{Temperature: temperature, MaxTokens: limit, LogProbs: req.Confidence})
	if err != nil {
		return llm.Result{}, err
	}
	res := llm.Result{JSON: c.Content}
	for _, t := range c.Tokens {
		res.Tokens = append(res.Tokens, llm.Token{Text: t.Text, Logprob: t.Logprob})
	}
	return res, nil
}
