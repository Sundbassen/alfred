package ministral

import (
	"context"
	"testing"

	"github.com/sundbassen/alfred/internal/assistant/llm"
	"github.com/sundbassen/alfred/internal/llm/llamaserver"
)

type fakeServer struct {
	messages []llamaserver.Message
	schema   any
	opts     llamaserver.Options
}

func (f *fakeServer) ChatJSON(_ context.Context, messages []llamaserver.Message, schema any, opts llamaserver.Options) (llamaserver.Completion, error) {
	f.messages, f.schema, f.opts = messages, schema, opts
	return llamaserver.Completion{Content: []byte(`{"intent":"none","slots":{}}`), Tokens: []llamaserver.Token{{Text: "{", Logprob: -0.1}}}, nil
}

func TestGenerate(t *testing.T) {
	srv := &fakeServer{}
	schema := &llm.Schema{Type: "object"}
	out, err := New(srv).Generate(context.Background(), llm.Request{System: "intents...", User: "turn the music off", Schema: schema})
	if err != nil || string(out.JSON) != `{"intent":"none","slots":{}}` || len(out.Tokens) != 1 {
		t.Fatalf("Generate = %+v, %v", out, err)
	}
	if len(srv.messages) != 2 || srv.messages[0] != (llamaserver.Message{Role: "system", Content: "intents..."}) ||
		srv.messages[1] != (llamaserver.Message{Role: "user", Content: "turn the music off"}) {
		t.Errorf("messages = %+v", srv.messages)
	}
	if srv.schema != schema || srv.opts.Temperature != 0 || srv.opts.MaxTokens != maxTokens || srv.opts.LogProbs {
		t.Errorf("schema %v, opts %+v", srv.schema, srv.opts)
	}
}

func TestGenerateOptions(t *testing.T) {
	srv := &fakeServer{}
	if _, err := New(srv).Generate(context.Background(), llm.Request{MaxTokens: 200, Confidence: true}); err != nil {
		t.Fatal(err)
	}
	if srv.opts.MaxTokens != 200 || !srv.opts.LogProbs {
		t.Errorf("opts %+v", srv.opts)
	}
}
