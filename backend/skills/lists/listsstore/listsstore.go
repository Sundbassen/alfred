// Package listsstore keeps the lists skill's lists in Alfred's local
// database (internal/store). It only runs the generated queries
// (internal/store/storedb, from internal/store/queries/lists.sql) and
// converts their rows to the skill's types; adding or removing several
// items is one transaction.
package listsstore

import (
	"context"
	"database/sql"
	"time"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
	"github.com/sundbassen/alfred/internal/store/storedb"
	"github.com/sundbassen/alfred/skills/lists"
)

// Store keeps lists in the database.
type Store struct {
	db *sql.DB
	q  *storedb.Queries
}

var _ lists.Store = (*Store)(nil)

// New returns a Store over a database opened by store.Open.
func New(db *sql.DB) *Store {
	return &Store{db: db, q: storedb.New(db)}
}

// Create creates a list, reporting false if it already exists.
func (s *Store) Create(ctx context.Context, name string) (bool, error) {
	n, err := s.q.CreateList(ctx, storedb.CreateListParams{Name: name, CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		return false, errors.WithMessagef(err, "create list %q", name)
	}
	return n > 0, nil
}

// Delete deletes a list and its items, reporting false if there was none.
func (s *Store) Delete(ctx context.Context, name string) (bool, error) {
	n, err := s.q.DeleteList(ctx, name)
	if err != nil {
		return false, errors.WithMessagef(err, "delete list %q", name)
	}
	return n > 0, nil
}

// Lists returns the lists with their item counts, oldest first.
func (s *Store) Lists(ctx context.Context) ([]lists.List, error) {
	rows, err := s.q.Lists(ctx)
	if err != nil {
		return nil, errors.WithMessage(err, "lists")
	}
	all := make([]lists.List, len(rows))
	for i, row := range rows {
		all[i] = lists.List{Name: row.Name, Items: int(row.Items)}
	}
	return all, nil
}

// Add adds items to a list in one transaction, returning the ones that
// were not on it yet; fault.ErrNotFound if the list doesn't exist.
func (s *Store) Add(ctx context.Context, list string, items []string) ([]string, error) {
	var added []string
	err := s.inList(ctx, list, func(q *storedb.Queries, id int64) error {
		now := time.Now().UnixMilli()
		for _, item := range items {
			n, err := q.AddItem(ctx, storedb.AddItemParams{ListID: id, Text: item, CreatedAt: now})
			if err != nil {
				return errors.WithMessagef(err, "add %q", item)
			}
			if n > 0 {
				added = append(added, item)
			}
		}
		return nil
	})
	return added, err
}

// Remove removes items from a list in one transaction, returning the ones
// that were on it; fault.ErrNotFound if the list doesn't exist.
func (s *Store) Remove(ctx context.Context, list string, items []string) ([]string, error) {
	var removed []string
	err := s.inList(ctx, list, func(q *storedb.Queries, id int64) error {
		for _, item := range items {
			n, err := q.RemoveItem(ctx, storedb.RemoveItemParams{ListID: id, Text: item})
			if err != nil {
				return errors.WithMessagef(err, "remove %q", item)
			}
			if n > 0 {
				removed = append(removed, item)
			}
		}
		return nil
	})
	return removed, err
}

// Items returns a list's items, oldest first; fault.ErrNotFound if the
// list doesn't exist.
func (s *Store) Items(ctx context.Context, list string) ([]string, error) {
	var items []string
	err := s.inList(ctx, list, func(q *storedb.Queries, id int64) error {
		var err error
		items, err = q.Items(ctx, id)
		return errors.WithMessage(err, "items")
	})
	return items, err
}

// Clear removes every item from a list, returning how many there were;
// fault.ErrNotFound if the list doesn't exist.
func (s *Store) Clear(ctx context.Context, list string) (int, error) {
	var n int64
	err := s.inList(ctx, list, func(q *storedb.Queries, id int64) error {
		var err error
		n, err = q.ClearList(ctx, id)
		return errors.WithMessage(err, "clear")
	})
	return int(n), err
}

// inList runs fn in a transaction with the list's ID; fault.ErrNotFound if
// there is no such list.
func (s *Store) inList(ctx context.Context, list string, fn func(q *storedb.Queries, id int64) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.WithStack(err)
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	id, err := q.ListID(ctx, list)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.WithMessagef(fault.ErrNotFound, "list %q", list)
	}
	if err != nil {
		return errors.WithMessagef(err, "find list %q", list)
	}
	if err := fn(q, id); err != nil {
		return err
	}
	return errors.WithStack(tx.Commit())
}
