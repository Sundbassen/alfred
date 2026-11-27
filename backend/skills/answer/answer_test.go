package answer

import (
	"context"
	"math"
	"strings"
	"testing"
	"testing/synctest"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/llm"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

// fakeModel answers with JSON and token probabilities.
type fakeModel struct {
	json   string
	tokens []llm.Token
	err    error
	req    llm.Request
}

func (f *fakeModel) Generate(_ context.Context, req llm.Request) (llm.Result, error) {
	f.req = req
	return llm.Result{JSON: []byte(f.json), Tokens: f.tokens}, f.err
}

// textTokens are the tokens of {"kind":"answer","text":"<words>"} with
// each word at probability p.
func textTokens(text string, p float64) []llm.Token {
	tokens := []llm.Token{{Text: `{"kind":"answer","text":"`, Logprob: 0}}
	for i, w := range strings.Fields(text) {
		if i > 0 {
			w = " " + w
		}
		tokens = append(tokens, llm.Token{Text: w, Logprob: math.Log(p)})
	}
	return append(tokens, llm.Token{Text: `"}`, Logprob: 0})
}

func ask(t *testing.T, m *fakeModel, question string) (string, error) {
	t.Helper()
	r := assistant.NewRouter()
	Register(r, m, nil)
	resp, err := r.Dispatch(context.Background(), Ask{Question: question})
	return resp.Text, err
}

func TestAnswers(t *testing.T) {
	for _, tt := range []struct {
		question string
		model    fakeModel
		want     string
	}{
		{"What is 15 times 23?", fakeModel{json: `{"kind":"math","expression":"15 * 23"}`}, "That's 345."},
		{"What is 1 divided by 0?", fakeModel{json: `{"kind":"math","expression":"1/0"}`}, "Sorry, I couldn't work that out."},
		{"How many grams in a pound?", fakeModel{json: `{"kind":"convert","value":1,"from":"lb","to":"g"}`}, "1 pound is 453.59 grams."},
		// The model swapped the units; the amount says which is which.
		{"What is 30 degrees Celsius in Fahrenheit?", fakeModel{json: `{"kind":"convert","value":30,"from":"f","to":"c"}`},
			"30 degrees Celsius is 86 degrees Fahrenheit."},
		{"How many kilos in a meter?", fakeModel{json: `{"kind":"convert","value":1,"from":"m","to":"kg"}`}, "Sorry, I can't convert between those."},
		{"Who wrote Hamlet?", fakeModel{json: `{"kind":"answer","text":"William Shakespeare wrote *Hamlet*"}`,
			tokens: textTokens("William Shakespeare wrote *Hamlet*", 0.95)}, "William Shakespeare wrote Hamlet."},
		{"Who was the mayor of Aarhus in 1923?", fakeModel{json: `{"kind":"answer","text":"Ole Olsen"}`,
			tokens: textTokens("Ole Olsen", 0.5)}, UnsureText},
		{"Tell me a joke", fakeModel{json: `{"kind":"unknown"}`}, UnknownText},
		// Swapped units with a decimal amount ("2.5" survives as a number).
		{"What is 2.5 miles in kilometers?", fakeModel{json: `{"kind":"convert","value":2.5,"from":"km","to":"mi"}`},
			"2.5 miles is 4.02 kilometers."},
		{"How many grams?", fakeModel{json: `{"kind":"convert","from":"lb","to":"g"}`}, "Sorry, I couldn't work that out."},
		{"How many millimeters in a lot of kilometers?", fakeModel{json: `{"kind":"convert","value":1e308,"from":"km","to":"mm"}`},
			"Sorry, I can't convert between those."},
		{"What is minus 2 squared?", fakeModel{json: `{"kind":"math","expression":"-2^2"}`}, "That's minus 4."},
		{"Who wrote Hamlet?", fakeModel{json: `{"kind":"answer","text":"Shakespeare"}`}, UnsureText},                   // no tokens
		{"Tell me everything about Rome", fakeModel{json: `{"kind":"answer","text":"Rome was founded in`}, UnsureText}, // cut off
		{"Who wrote Hamlet?", fakeModel{json: `{"kind":"answer","text":"  "}`, tokens: textTokens("x", 0.99)}, UnsureText},
	} {
		m := tt.model
		got, err := ask(t, &m, tt.question)
		if err != nil || got != tt.want {
			t.Errorf("%q: %q, %v; want %q", tt.question, got, err, tt.want)
		}
		if !m.req.Confidence || m.req.Schema == nil || len(m.req.Schema.AnyOf) != 4 || m.req.User != tt.question {
			t.Errorf("%q: request %+v", tt.question, m.req)
		}
	}
}

func TestOneUnlikelyWordIsEnough(t *testing.T) {
	tokens := textTokens("The tallest mountain in Denmark is Hvidmøllen", 0.97)
	tokens[len(tokens)-2].Logprob = math.Log(0.08) // the made-up name
	m := &fakeModel{json: `{"kind":"answer","text":"The tallest mountain in Denmark is Hvidmøllen"}`, tokens: tokens}
	if got, _ := ask(t, m, "What's the tallest mountain in Denmark?"); got != UnsureText {
		t.Errorf("%q, want %q", got, UnsureText)
	}
}

func TestModelErrors(t *testing.T) {
	if _, err := ask(t, &fakeModel{err: errors.WithMessage(fault.ErrServiceUnavailable, "loading")}, "x"); !errors.Is(err, fault.ErrServiceUnavailable) {
		t.Errorf("unavailable: %v", err)
	}
	if _, err := ask(t, &fakeModel{json: "nope"}, "x"); !errors.Is(err, fault.ErrDecoding) {
		t.Errorf("bad JSON: %v", err)
	}
}

// blockingModel answers only when its context ends.
type blockingModel struct{}

func (blockingModel) Generate(ctx context.Context, _ llm.Request) (llm.Result, error) {
	<-ctx.Done()
	return llm.Result{}, ctx.Err()
}

func TestTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := assistant.NewRouter()
		Register(r, blockingModel{}, nil)
		_, err := r.Dispatch(context.Background(), Ask{Question: "What is the meaning of life?"})
		if !errors.Is(err, fault.ErrTimeout) {
			t.Errorf("slow model: %v, want ErrTimeout", err)
		}
	})
}
