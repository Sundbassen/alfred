package listsstore

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/store"
	"github.com/sundbassen/alfred/skills/lists"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "alfred.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

func TestLists(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if created, err := s.Create(ctx, "shopping"); !created || err != nil {
		t.Fatalf("Create = %v, %v", created, err)
	}
	if created, err := s.Create(ctx, "shopping"); created || err != nil {
		t.Errorf("Create twice = %v, %v; want false", created, err)
	}
	s.Create(ctx, "packing")

	added, err := s.Add(ctx, "shopping", []string{"milk", "eggs"})
	if err != nil || !slices.Equal(added, []string{"milk", "eggs"}) {
		t.Fatalf("Add = %q, %v", added, err)
	}
	if added, _ := s.Add(ctx, "shopping", []string{"eggs", "bread"}); !slices.Equal(added, []string{"bread"}) {
		t.Errorf("Add with a duplicate = %q, want [bread]", added)
	}
	if items, _ := s.Items(ctx, "shopping"); !slices.Equal(items, []string{"milk", "eggs", "bread"}) {
		t.Errorf("Items = %q", items)
	}
	if removed, _ := s.Remove(ctx, "shopping", []string{"eggs", "cheese"}); !slices.Equal(removed, []string{"eggs"}) {
		t.Errorf("Remove = %q, want [eggs]", removed)
	}
	all, err := s.Lists(ctx)
	if err != nil || !slices.Equal(all, []lists.List{{Name: "shopping", Items: 2}, {Name: "packing", Items: 0}}) {
		t.Errorf("Lists = %+v, %v", all, err)
	}
	if n, _ := s.Clear(ctx, "shopping"); n != 2 {
		t.Errorf("Clear = %d, want 2", n)
	}

	// Deleting a list deletes its items (ON DELETE CASCADE).
	s.Add(ctx, "packing", []string{"socks"})
	if deleted, err := s.Delete(ctx, "packing"); !deleted || err != nil {
		t.Fatalf("Delete = %v, %v", deleted, err)
	}
	s.Create(ctx, "packing")
	if items, _ := s.Items(ctx, "packing"); len(items) != 0 {
		t.Errorf("recreated list has %q", items)
	}
	if deleted, _ := s.Delete(ctx, "camping"); deleted {
		t.Error("deleted a list that doesn't exist")
	}

	for name, call := range map[string]func() error{
		"Add":    func() error { _, err := s.Add(ctx, "camping", []string{"tent"}); return err },
		"Remove": func() error { _, err := s.Remove(ctx, "camping", []string{"tent"}); return err },
		"Items":  func() error { _, err := s.Items(ctx, "camping"); return err },
		"Clear":  func() error { _, err := s.Clear(ctx, "camping"); return err },
	} {
		if err := call(); !errors.Is(err, fault.ErrNotFound) {
			t.Errorf("%s on a missing list: %v, want ErrNotFound", name, err)
		}
	}
}
