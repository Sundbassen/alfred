package llm

import (
	"encoding/json"
	"math"
	"testing"
)

func toks(pairs ...any) []Token {
	var ts []Token
	for i := 0; i < len(pairs); i += 2 {
		ts = append(ts, Token{Text: pairs[i].(string), Logprob: math.Log(pairs[i+1].(float64))})
	}
	return ts
}

func TestTextConfidence(t *testing.T) {
	// As llama-server returns them: a ```json fence the content doesn't
	// have, JSON structure, and the answer's tokens with formatting.
	tokens := toks("```", 0.99, "json", 1.0, "\n{\n", 0.99, ` "`, 0.99, "kind", 0.9, `":`, 1.0, ` "`, 1.0,
		"answer", 0.99, "\",\n", 1.0, ` "`, 0.99, "text", 0.25, `":`, 1.0, ` "`, 0.96,
		"William", 0.8, " Shakespeare", 1.0, " *", 0.4, "Ham", 1.0, "let", 1.0, "*.", 0.6, "\"\n", 0.98, "}", 1.0)
	mean, least, ok := TextConfidence(tokens, "text")
	if !ok {
		t.Fatal("no text field found")
	}
	// Only William, Shakespeare, Ham, let count: not the structure, the
	// low-probability key "text", or the "*" formatting.
	if want := math.Pow(0.8, 1.0/4); math.Abs(mean-want) > 1e-9 || math.Abs(least-0.8) > 1e-9 {
		t.Errorf("mean %.3f least %.3f, want %.3f and 0.8", mean, least, want)
	}
	if _, _, ok := TextConfidence(tokens, "missing"); ok {
		t.Error("found a field that isn't there")
	}
	if _, _, ok := TextConfidence(toks(`{"text": "`, 1.0, "...", 0.5, `"}`, 1.0), "text"); ok {
		t.Error("a value without letters or digits has no confidence")
	}
}

func TestSchemaPropertyOrder(t *testing.T) {
	no := false
	s := &Schema{AnyOf: []*Schema{{
		Type: "object", AdditionalProperties: &no, Required: []string{"kind", "expression"}, Order: []string{"kind"},
		Properties: map[string]*Schema{"expression": {Type: "string"}, "kind": {Type: "string", Enum: []string{"math"}}, "a": {Type: "number"}},
	}}}
	data, err := json.Marshal(s)
	want := `{"anyOf":[{"properties":{"kind":{"type":"string","enum":["math"]},"a":{"type":"number"},"expression":{"type":"string"}},` +
		`"type":"object","required":["kind","expression"],"additionalProperties":false}]}`
	if err != nil || string(data) != want {
		t.Errorf("JSON:\n got %s\nwant %s", data, want)
	}
}
