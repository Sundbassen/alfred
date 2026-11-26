package lists

import (
	"context"
	"slices"
	"testing"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/app"
	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/assistant/match"
	"github.com/sundbassen/alfred/internal/platform/fault"
)

// fakeStore keeps lists in memory, like listsstore in the database.
type fakeStore struct {
	order []string
	items map[string][]string
}

func newFakeStore() *fakeStore { return &fakeStore{items: make(map[string][]string)} }

func (s *fakeStore) Create(_ context.Context, name string) (bool, error) {
	if _, ok := s.items[name]; ok {
		return false, nil
	}
	s.order = append(s.order, name)
	s.items[name] = nil
	return true, nil
}

func (s *fakeStore) Delete(_ context.Context, name string) (bool, error) {
	if _, ok := s.items[name]; !ok {
		return false, nil
	}
	delete(s.items, name)
	s.order = slices.DeleteFunc(s.order, func(n string) bool { return n == name })
	return true, nil
}

func (s *fakeStore) Lists(context.Context) ([]List, error) {
	var all []List
	for _, n := range s.order {
		all = append(all, List{Name: n, Items: len(s.items[n])})
	}
	return all, nil
}

func (s *fakeStore) Add(_ context.Context, list string, items []string) ([]string, error) {
	cur, ok := s.items[list]
	if !ok {
		return nil, errors.WithMessage(fault.ErrNotFound, list)
	}
	var added []string
	for _, it := range items {
		if !slices.Contains(cur, it) {
			cur = append(cur, it)
			added = append(added, it)
		}
	}
	s.items[list] = cur
	return added, nil
}

func (s *fakeStore) Remove(_ context.Context, list string, items []string) ([]string, error) {
	cur, ok := s.items[list]
	if !ok {
		return nil, errors.WithMessage(fault.ErrNotFound, list)
	}
	var removed []string
	for _, it := range items {
		if i := slices.Index(cur, it); i >= 0 {
			cur = slices.Delete(cur, i, i+1)
			removed = append(removed, it)
		}
	}
	s.items[list] = cur
	return removed, nil
}

func (s *fakeStore) Items(_ context.Context, list string) ([]string, error) {
	cur, ok := s.items[list]
	if !ok {
		return nil, errors.WithMessage(fault.ErrNotFound, list)
	}
	return cur, nil
}

func (s *fakeStore) Clear(_ context.Context, list string) (int, error) {
	cur, ok := s.items[list]
	if !ok {
		return 0, errors.WithMessage(fault.ErrNotFound, list)
	}
	s.items[list] = nil
	return len(cur), nil
}

type conversation struct {
	t     *testing.T
	a     *app.Assistant
	store *fakeStore
	now   time.Time
}

func newConversation(t *testing.T) *conversation {
	c := &conversation{t: t, store: newFakeStore(), now: time.Date(2026, 11, 24, 10, 0, 0, 0, time.Local)}
	r := assistant.NewRouter()
	register(r, c.store, func() time.Time { return c.now })
	c.a = app.NewAssistant(r, match.New(r.Intents()))
	return c
}

// say returns Alfred's answer to a transcript.
func (c *conversation) say(transcript string) string {
	c.t.Helper()
	reply, err := c.a.Respond(context.Background(), transcript)
	if err != nil {
		return assistant.SpokenText(err)
	}
	return reply.Text
}

func (c *conversation) expect(steps ...string) {
	c.t.Helper()
	for i := 0; i+1 < len(steps); i += 2 {
		if got := c.say(steps[i]); got != steps[i+1] {
			c.t.Errorf("%q:\n got %q\nwant %q", steps[i], got, steps[i+1])
		}
	}
}

func TestCreateAddRead(t *testing.T) {
	c := newConversation(t)
	c.expect(
		"What's on my shopping list?", "You don't have a list called shopping.",
		"Add milk to the list.", "You don't have any lists yet. Say: create a list called shopping.",
		"Create a list called shopping.", "Created your shopping list.",
		"Make a new list called shopping.", "You already have your shopping list.",
		"Add milk, eggs and bread to the shopping list.", "Added milk, eggs and bread to your shopping list.",
		"Add milk to my shopping list.", "Milk is already on your shopping list.",
		"Add eggs and an apple to it.", "Added apple to your shopping list. Eggs was already on it.",
		"Remove the apple from the list.", "Removed apple from your shopping list.",
		"Put cheese on the shopping list.", "Added cheese to your shopping list.",
		"What's on my shopping list?", "Your shopping list has 4 items: milk, eggs, bread and cheese.",
		"Remove eggs and cheese from the shopping list.", "Removed eggs and cheese from your shopping list.",
		"Take apples off my shopping list.", "Apples isn't on your shopping list.",
		"What lists do I have?", "You have one list: shopping.",
	)
}

func TestWhichList(t *testing.T) {
	c := newConversation(t)
	c.expect(
		"Create a list called shopping.", "Created your shopping list.",
		"Start a packing list.", "Created your packing list.",
		// Just used: "add to it" goes to the packing list.
		"Add socks to it.", "Added socks to your packing list.",
	)
	c.now = c.now.Add(10 * time.Minute)
	c.expect(
		"Add milk to the list.", "Add to which list? You have shopping and packing.",
		"The shopping list.", "Added milk to your shopping list.",
		"What lists do I have?", "You have 2 lists: shopping and packing.",
	)
}

func TestUnknownListAsksToCreate(t *testing.T) {
	c := newConversation(t)
	c.expect(
		"Add a tent to the camping list.", "You don't have a list called camping. Should I create it?",
		"Yes.", "Added tent to your camping list.",
		"Add stove to the hiking list.", "You don't have a list called hiking. Should I create it?",
		"No.", "OK.",
		"What lists do I have?", "You have one list: camping.",
	)
}

func TestClearAndDeleteConfirm(t *testing.T) {
	c := newConversation(t)
	c.expect(
		"Create a list called shopping.", "Created your shopping list.",
		"Clear the shopping list.", "Your shopping list is already empty.",
		"Add milk and eggs to the shopping list.", "Added milk and eggs to your shopping list.",
		"Clear the shopping list.", "Remove all 2 items from your shopping list?",
		"No.", "OK.",
		"Clear my shopping list.", "Remove all 2 items from your shopping list?",
		"Yes.", "Cleared your shopping list.",
		"What's on the shopping list?", "Your shopping list is empty.",
		"Add milk to the shopping list.", "Added milk to your shopping list.",
		"Clear the shopping list.", "Remove milk from your shopping list?",
		"No.", "OK.",
		"Delete the shopping list.", "Delete your shopping list and its 1 item?",
		"Yes.", "Deleted your shopping list.",
		"Delete the shopping list.", "You don't have a list called shopping.",
		// "Delete the list": the one just used or the only one, always
		// confirmed.
		"Create a list called packing.", "Created your packing list.",
		"Delete the list.", "Delete your packing list?",
		"Yes.", "Deleted your packing list.",
		"Delete the list.", "You don't have any lists yet. Say: create a list called shopping.",
	)
}

func TestNameAndItems(t *testing.T) {
	for said, want := range map[string]string{
		"shopping": "shopping", "shopping list": "shopping", "the shopping list": "shopping",
		"my shopping lists": "shopping", "to do": "to do", "list": "", "this": "", "that list": "",
	} {
		if got := Name(said); got != want {
			t.Errorf("Name(%q) = %q, want %q", said, got, want)
		}
	}
	if got := splitItems("milk, eggs, and bread, some apples and milk"); !slices.Equal(got, []string{"milk", "eggs", "bread", "apples"}) {
		t.Errorf("splitItems = %q", got)
	}
}
