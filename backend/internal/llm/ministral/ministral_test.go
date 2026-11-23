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

func (f *fakeServer) ChatJSON(_ context.Context, messages []llamaserver.Message, schema any, opts llamaserver.Options) ([]byte, error) {
	f.messages, f.schema, f.opts = messages, schema, opts
	return []byte(`{"intent":"none","slots":{}}`), nil
}

func TestGenerate(t *testing.T) {
	srv := &fakeServer{}
	schema := &llm.Schema{Type: "object"}
	out, err := New(srv).Generate(context.Background(), llm.Request{System: "intents...", User: "turn the music off", Schema: schema})
	if err != nil || string(out) != `{"intent":"none","slots":{}}` {
		t.Fatalf("Generate = %s, %v", out, err)
	}
	if len(srv.messages) != 2 || srv.messages[0] != (llamaserver.Message{Role: "system", Content: "intents..."}) ||
		srv.messages[1] != (llamaserver.Message{Role: "user", Content: "turn the music off"}) {
		t.Errorf("messages = %+v", srv.messages)
	}
	if srv.schema != schema || srv.opts.Temperature != 0 || srv.opts.MaxTokens != maxTokens {
		t.Errorf("schema %v, opts %+v", srv.schema, srv.opts)
	}
}
