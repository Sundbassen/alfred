// Package lists is the lists skill: lists the user creates and fills by
// voice ("create a list called packing", "add milk, eggs and bread to the
// shopping list", "what's on my shopping list"). The lists live behind the
// Store interface: in the local database for now (listsstore), in the cloud
// once there is one (#39).
package lists

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/assistant"
	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/spoken"
)

// List is a list and how many items it has.
type List struct {
	Name  string
	Items int
}

// Store keeps the lists. Names and items are normalized (lowercase, see
// Name and splitItems). Operations on a list that doesn't exist return
// fault.ErrNotFound.
type Store interface {
	// Create creates a list, reporting false if it already exists.
	Create(ctx context.Context, name string) (bool, error)
	// Delete deletes a list and its items, reporting false if there was none.
	Delete(ctx context.Context, name string) (bool, error)
	// Lists returns the lists, oldest first.
	Lists(ctx context.Context) ([]List, error)
	// Add adds items, returning the ones that were not on the list yet.
	Add(ctx context.Context, list string, items []string) ([]string, error)
	// Remove removes items, returning the ones that were on the list.
	Remove(ctx context.Context, list string, items []string) ([]string, error)
	// Items returns a list's items, oldest first.
	Items(ctx context.Context, list string) ([]string, error)
	// Clear removes every item, returning how many there were.
	Clear(ctx context.Context, list string) (int, error)
}

// Create is "create a list called packing".
type Create struct {
	List string `slot:"list"`
}

func (Create) IntentName() string { return "list_create" }

// Add is "add milk, eggs and bread to the shopping list"; without a list,
// the one just used, the only one, or Alfred asks.
type Add struct {
	Items string `slot:"items"`
	List  string `slot:"list,optional"`
	// CreateList is set on the intent of "Should I create it?".
	CreateList bool
}

func (Add) IntentName() string { return "list_add" }

// Remove is "remove milk from the shopping list".
type Remove struct {
	Items string `slot:"items"`
	List  string `slot:"list,optional"`
}

func (Remove) IntentName() string { return "list_remove" }

// Read is "what's on my shopping list".
type Read struct {
	List string `slot:"list,optional"`
}

func (Read) IntentName() string { return "list_read" }

// Clear is "clear the shopping list".
type Clear struct {
	List      string `slot:"list,optional"`
	Confirmed bool
}

func (Clear) IntentName() string { return "list_clear" }

// Delete is "delete the packing list".
type Delete struct {
	List      string `slot:"list"`
	Confirmed bool
}

func (Delete) IntentName() string { return "list_delete" }

// ListLists is "what lists do I have".
type ListLists struct{}

func (ListLists) IntentName() string { return "lists" }

// recent is how long "add milk" without a list goes to the list just used.
const recent = 5 * time.Minute

// Register adds the list intents to r, keeping the lists in store.
func Register(r *assistant.Router, store Store) {
	register(r, store, time.Now)
}

func register(r *assistant.Router, store Store, now func() time.Time) {
	h := &handler{store: store, now: now}
	assistant.Handle(r, assistant.Spec{
		Description: "Create a new named list",
		Phrases: []string{
			"create a list called {list}", "create a new list called {list}", "make a list called {list}",
			"make a new list called {list}", "start a {list} list", "create a {list} list", "new list called {list}",
		},
		Help: "keep lists",
	}, h.create)
	assistant.Handle(r, assistant.Spec{
		Description: "Add one or more items to a list",
		Phrases: []string{
			"add {items} to the {list}", "add {items} to my {list}", "add {items} to {list}",
			"put {items} on the {list}", "put {items} on my {list}", "add {items}",
		},
	}, h.add)
	assistant.Handle(r, assistant.Spec{
		Description: "Remove one or more items from a list",
		Phrases: []string{
			"remove {items} from the {list}", "remove {items} from my {list}",
			"take {items} off the {list}", "take {items} off my {list}",
			"cross {items} off the {list}", "cross {items} off my {list}", "remove {items}",
		},
	}, h.remove)
	assistant.Handle(r, assistant.Spec{
		Description: "Read out what is on a list",
		Phrases:     []string{"what is on the {list}", "what is on my {list}", "read the {list}", "read my {list}"},
	}, h.read)
	assistant.Handle(r, assistant.Spec{
		Description: "Remove every item from a list",
		Phrases:     []string{"clear the {list}", "clear my {list}", "empty the {list}", "empty my {list}"},
	}, h.clear)
	assistant.Handle(r, assistant.Spec{
		Description: "Delete a list and its items",
		// Ending in "list" beats the clock's "delete the {what}" and
		// "remove {items}".
		Phrases: []string{
			"delete the {list} list", "delete my {list} list",
			"remove the {list} list", "remove my {list} list",
		},
	}, h.delete)
	assistant.Handle(r, assistant.Spec{
		Description: "Say which lists there are",
		Phrases:     []string{"what lists do i have", "which lists do i have", "what are my lists", "list my lists"},
	}, h.lists)
}

type handler struct {
	store Store
	now   func() time.Time

	mu       sync.Mutex
	lastList string // the list used last, for "add milk"
	lastUsed time.Time
}

func (h *handler) used(name string) {
	h.mu.Lock()
	h.lastList, h.lastUsed = name, h.now()
	h.mu.Unlock()
}

// resolve finds the list an intent means: the one named, else the one just
// used, else the only one. ask is the question to ask otherwise; ok is
// false when the reply (resp) says why there is no list.
func (h *handler) resolve(ctx context.Context, said string, ask func(lists []List) assistant.Response) (name string, resp assistant.Response, ok bool, err error) {
	if name = Name(said); name != "" {
		return name, assistant.Response{}, true, nil
	}
	h.mu.Lock()
	last, lastUsed := h.lastList, h.lastUsed
	h.mu.Unlock()
	if last != "" && h.now().Sub(lastUsed) <= recent {
		return last, assistant.Response{}, true, nil
	}
	all, err := h.store.Lists(ctx)
	if err != nil {
		return "", assistant.Response{}, false, err
	}
	switch len(all) {
	case 0:
		return "", assistant.Response{Text: "You don't have any lists yet. Say: create a list called shopping."}, false, nil
	case 1:
		return all[0].Name, assistant.Response{}, true, nil
	}
	return "", ask(all), false, nil
}

// whichList asks which list, answered into the intent's List slot.
func whichList(intent assistant.Intent, verb string) func([]List) assistant.Response {
	return func(all []List) assistant.Response {
		names := make([]string, len(all))
		for i, l := range all {
			names[i] = l.Name
		}
		return assistant.Response{
			Text: fmt.Sprintf("%s which list? You have %s.", verb, spoken.JoinAnd(names)),
			Ask:  &assistant.Question{Intent: intent, Slot: "list"},
		}
	}
}

func (h *handler) create(ctx context.Context, in Create) (assistant.Response, error) {
	name := Name(in.List)
	if name == "" {
		return assistant.Response{}, &assistant.SpokenError{Text: "Sorry, what should the list be called?",
			Err: errors.WithMessagef(fault.ErrInvalidValue, "list name %q", in.List)}
	}
	created, err := h.store.Create(ctx, name)
	if err != nil {
		return assistant.Response{}, err
	}
	h.used(name)
	if !created {
		return assistant.Response{Text: "You already have " + your(name) + "."}, nil
	}
	return assistant.Response{Text: "Created " + your(name) + "."}, nil
}

func (h *handler) add(ctx context.Context, in Add) (assistant.Response, error) {
	items := splitItems(in.Items)
	if len(items) == 0 {
		return assistant.Response{}, &assistant.SpokenError{Text: "Sorry, what should I add?",
			Err: errors.WithMessagef(fault.ErrInvalidValue, "items %q", in.Items)}
	}
	name, resp, ok, err := h.resolve(ctx, in.List, whichList(in, "Add to"))
	if err != nil || !ok {
		return resp, err
	}
	if in.CreateList {
		if _, err := h.store.Create(ctx, name); err != nil {
			return assistant.Response{}, err
		}
	}
	added, err := h.store.Add(ctx, name, items)
	if errors.Is(err, fault.ErrNotFound) {
		in.List, in.CreateList = name, true
		return assistant.Response{
			Text: "You don't have " + aList(name) + ". Should I create it?",
			Ask:  &assistant.Question{Intent: in},
		}, nil
	}
	if err != nil {
		return assistant.Response{}, err
	}
	h.used(name)
	switch {
	case len(added) == 0 && len(items) == 1:
		return assistant.Response{Text: capitalize(items[0]) + " is already on " + your(name) + "."}, nil
	case len(added) == 0:
		return assistant.Response{Text: "They're already on " + your(name) + "."}, nil
	}
	return assistant.Response{Text: "Added " + spoken.JoinAnd(added) + " to " + your(name) + "."}, nil
}

func (h *handler) remove(ctx context.Context, in Remove) (assistant.Response, error) {
	items := splitItems(in.Items)
	if len(items) == 0 {
		return assistant.Response{}, &assistant.SpokenError{Text: "Sorry, what should I remove?",
			Err: errors.WithMessagef(fault.ErrInvalidValue, "items %q", in.Items)}
	}
	name, resp, ok, err := h.resolve(ctx, in.List, whichList(in, "Remove from"))
	if err != nil || !ok {
		return resp, err
	}
	removed, err := h.store.Remove(ctx, name, items)
	if errors.Is(err, fault.ErrNotFound) {
		return assistant.Response{Text: "You don't have " + aList(name) + "."}, nil
	}
	if err != nil {
		return assistant.Response{}, err
	}
	h.used(name)
	if len(removed) == 0 {
		return assistant.Response{Text: capitalize(spoken.JoinOr(items)) + " isn't on " + your(name) + "."}, nil
	}
	return assistant.Response{Text: "Removed " + spoken.JoinAnd(removed) + " from " + your(name) + "."}, nil
}

func (h *handler) read(ctx context.Context, in Read) (assistant.Response, error) {
	name, resp, ok, err := h.resolve(ctx, in.List, whichList(in, "Read"))
	if err != nil || !ok {
		return resp, err
	}
	items, err := h.store.Items(ctx, name)
	if errors.Is(err, fault.ErrNotFound) {
		return assistant.Response{Text: "You don't have " + aList(name) + "."}, nil
	}
	if err != nil {
		return assistant.Response{}, err
	}
	h.used(name)
	switch len(items) {
	case 0:
		return assistant.Response{Text: capitalize(your(name)) + " is empty."}, nil
	case 1:
		return assistant.Response{Text: capitalize(your(name)) + " has 1 item: " + items[0] + "."}, nil
	}
	return assistant.Response{Text: fmt.Sprintf("%s has %d items: %s.", capitalize(your(name)), len(items), spoken.JoinAnd(items))}, nil
}

func (h *handler) clear(ctx context.Context, in Clear) (assistant.Response, error) {
	name, resp, ok, err := h.resolve(ctx, in.List, whichList(in, "Clear"))
	if err != nil || !ok {
		return resp, err
	}
	items, err := h.store.Items(ctx, name)
	if errors.Is(err, fault.ErrNotFound) {
		return assistant.Response{Text: "You don't have " + aList(name) + "."}, nil
	}
	if err != nil {
		return assistant.Response{}, err
	}
	if len(items) == 0 {
		return assistant.Response{Text: capitalize(your(name)) + " is already empty."}, nil
	}
	if !in.Confirmed {
		in.List, in.Confirmed = name, true
		return assistant.Response{
			Text: fmt.Sprintf("Remove all %s from %s?", plural(len(items), "item"), your(name)),
			Ask:  &assistant.Question{Intent: in},
		}, nil
	}
	if _, err := h.store.Clear(ctx, name); err != nil {
		return assistant.Response{}, err
	}
	h.used(name)
	return assistant.Response{Text: "Cleared " + your(name) + "."}, nil
}

func (h *handler) delete(ctx context.Context, in Delete) (assistant.Response, error) {
	name := Name(in.List)
	items, err := h.store.Items(ctx, name)
	if errors.Is(err, fault.ErrNotFound) {
		return assistant.Response{Text: "You don't have " + aList(name) + "."}, nil
	}
	if err != nil {
		return assistant.Response{}, err
	}
	if len(items) > 0 && !in.Confirmed {
		in.List, in.Confirmed = name, true
		return assistant.Response{
			Text: fmt.Sprintf("Delete %s and its %s?", your(name), plural(len(items), "item")),
			Ask:  &assistant.Question{Intent: in},
		}, nil
	}
	if _, err := h.store.Delete(ctx, name); err != nil {
		return assistant.Response{}, err
	}
	h.mu.Lock()
	if h.lastList == name {
		h.lastList = ""
	}
	h.mu.Unlock()
	return assistant.Response{Text: "Deleted " + your(name) + "."}, nil
}

func (h *handler) lists(ctx context.Context, _ ListLists) (assistant.Response, error) {
	all, err := h.store.Lists(ctx)
	if err != nil {
		return assistant.Response{}, err
	}
	names := make([]string, len(all))
	for i, l := range all {
		names[i] = l.Name
	}
	switch len(all) {
	case 0:
		return assistant.Response{Text: "You don't have any lists yet."}, nil
	case 1:
		return assistant.Response{Text: "You have one list: " + names[0] + "."}, nil
	}
	return assistant.Response{Text: fmt.Sprintf("You have %d lists: %s.", len(all), spoken.JoinAnd(names))}, nil
}

// Name normalizes a spoken list name: "the shopping list", "my shopping
// lists" and "shopping" are all "shopping". "" means no name was said.
func Name(said string) string {
	words := strings.Fields(strings.ToLower(strings.ReplaceAll(said, ",", " ")))
	for len(words) > 0 && (words[0] == "the" || words[0] == "my" || words[0] == "our" || words[0] == "a") {
		words = words[1:]
	}
	for len(words) > 0 && (words[len(words)-1] == "list" || words[len(words)-1] == "lists") {
		words = words[:len(words)-1]
	}
	return strings.Join(words, " ")
}

// splitItems splits "milk, eggs and bread" into its items.
func splitItems(text string) []string {
	text = strings.ReplaceAll(strings.ToLower(text), " and ", ",")
	var items []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(text, ",") {
		words := strings.Fields(part)
		if len(words) > 0 && (words[0] == "and" || words[0] == "some") {
			words = words[1:]
		}
		item := strings.Join(words, " ")
		if item != "" && !seen[item] {
			seen[item] = true
			items = append(items, item)
		}
	}
	return items
}

func your(name string) string  { return "your " + name + " list" }
func aList(name string) string { return "a list called " + name }

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
