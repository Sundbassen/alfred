package app

import (
	"context"
	"testing"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

type addItem struct {
	Item string `slot:"item"`
	List string `slot:"list,optional"`
}

func (addItem) IntentName() string { return "add" }

type createList struct {
	Name      string `slot:"name"`
	Confirmed bool   // set on the intent the question carries
}

func (createList) IntentName() string { return "create" }

// newAskingAssistant has "good morning" plus two intents that ask
// follow-up questions.
func newAskingAssistant(t *testing.T) (*Assistant, *[]string) {
	t.Helper()
	var did []string
	r := newTestRouter(nil)
	assistant.Handle(r, assistant.Spec{Phrases: []string{"add {item}"}},
		func(_ context.Context, in addItem) (assistant.Response, error) {
			if in.List == "" {
				return assistant.Response{Text: "Add to which list?",
					Ask: &assistant.Question{Intent: in, Slot: "list"}}, nil
			}
			did = append(did, "add "+in.Item+" to "+in.List)
			return assistant.Response{Text: "Added."}, nil
		})
	assistant.Handle(r, assistant.Spec{Phrases: []string{"create {name}"}},
		func(_ context.Context, in createList) (assistant.Response, error) {
			if !in.Confirmed {
				in.Confirmed = true
				return assistant.Response{Text: "Should I create it?", Ask: &assistant.Question{Intent: in}}, nil
			}
			did = append(did, "created "+in.Name)
			return assistant.Response{Text: "Created."}, nil
		})
	return withTemplates(r), &did
}

func respond(t *testing.T, a *Assistant, transcript string) *Reply {
	t.Helper()
	reply, err := a.Respond(context.Background(), transcript)
	if err != nil {
		t.Fatalf("Respond(%q): %v", transcript, err)
	}
	return reply
}

func TestSlotQuestion(t *testing.T) {
	a, did := newAskingAssistant(t)
	if reply := respond(t, a, "Add milk."); reply.Text != "Add to which list?" || !reply.Listen {
		t.Fatalf("question: %+v", reply)
	}
	reply := respond(t, a, "The shopping list.")
	if reply.Text != "Added." || reply.Listen || reply.Recognition.By != FollowUpBy {
		t.Errorf("answer: %+v", reply)
	}
	if len(*did) != 1 || (*did)[0] != "add milk to the shopping list" {
		t.Errorf("did %q", *did)
	}
	// Answered once: the same words now are no answer, and match nothing.
	if _, err := a.Respond(context.Background(), "The shopping list."); !errors.Is(err, fault.ErrNotFound) {
		t.Errorf("second answer: %v, want no match", err)
	}
}

func TestYesNoQuestion(t *testing.T) {
	a, did := newAskingAssistant(t)
	respond(t, a, "Create packing.")
	if reply := respond(t, a, "No."); reply.Text != "OK." {
		t.Errorf("no: %+v", reply)
	}

	respond(t, a, "Create packing.")
	if reply := respond(t, a, "Yes, please."); reply.Text != "Created." {
		t.Errorf("yes: %+v", reply)
	}
	if (*did)[len(*did)-1] != "created packing" {
		t.Errorf("did %q", *did)
	}

	// Something that is neither yes nor no is handled as usual.
	respond(t, a, "Create camping.")
	if _, err := a.Respond(context.Background(), "maybe tomorrow"); !errors.Is(err, fault.ErrNotFound) {
		t.Errorf("neither yes nor no: %v, want no match", err)
	}
}

func TestCommandInsteadOfAnswer(t *testing.T) {
	a, did := newAskingAssistant(t)
	respond(t, a, "Add milk.")
	if reply := respond(t, a, "Good morning."); reply.Text != "Good morning!" || reply.Recognition.Intent != "greet" {
		t.Errorf("command instead of answer: %+v", reply)
	}
	if len(*did) != 0 {
		t.Errorf("did %q, want nothing added", *did)
	}
}

func TestQuestionExpires(t *testing.T) {
	a, did := newAskingAssistant(t)
	now := time.Now()
	a.now = func() time.Time { return now }
	respond(t, a, "Add milk.")
	now = now.Add(QuestionTTL + time.Second)
	if _, err := a.Respond(context.Background(), "The shopping list."); !errors.Is(err, fault.ErrNotFound) {
		t.Errorf("late answer: %v, want no match", err)
	}
	if len(*did) != 0 {
		t.Errorf("did %q", *did)
	}
}
