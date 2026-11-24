// Package store is Alfred's local database: one SQLite file on the speaker
// for state it must keep across restarts, such as timers (see #37). User
// data shared across devices (lists, preferences) belongs to the cloud
// backend instead (#39).
//
// The schema is shared by all skills, so foreign keys and joins can span
// them: numbered, up-only migrations in migrations/, applied by Open. Every
// table is STRICT. The queries are here too (queries/, one file per table
// or area); sqlc generates them, over the migrations, into one package,
// storedb (backend/sqlc.yaml). Skills use it through their own <skill>store
// package, which converts the rows to the skill's domain types.
package store

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gitlab.com/tozd/go/errors"
	_ "modernc.org/sqlite" // the "sqlite" database/sql driver, pure Go

	"github.com/sundbassen/alfred/internal/platform/fault"
)

//go:embed migrations/*.sql
var migrations embed.FS

// pragmas apply to every connection of the pool: foreign keys are off by
// default in SQLite and per connection; WAL lets reads run alongside the
// writer; busy_timeout waits for a lock instead of failing.
const pragmas = "_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

// DefaultPath is $XDG_STATE_HOME/alfred/alfred.db, by default
// ~/.local/state/alfred/alfred.db.
func DefaultPath() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "alfred", "alfred.db")
}

// Open opens (creating if needed) the database at path and applies pending
// migrations. The directory is created with mode 0700 and the file with
// 0600.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errors.WithMessage(err, "create database directory")
	}
	// Create the file ourselves so it gets 0600, not SQLite's 0644.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, errors.WithMessage(err, "create database file")
	}
	if err := f.Close(); err != nil {
		return nil, errors.WithStack(err)
	}

	db, err := sql.Open("sqlite", "file:"+path+"?"+pragmas)
	if err != nil {
		return nil, errors.WithMessagef(err, "open database %s", path)
	}
	if err := migrate(ctx, db, migrations); err != nil {
		db.Close()
		return nil, errors.WithMessagef(err, "migrate database %s", path)
	}
	return db, nil
}

// migration is one numbered schema change.
type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads migrations/NNNN_name.sql, which must be numbered 1,
// 2, 3, … without gaps.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, "migrations/*.sql")
	if err != nil {
		return nil, errors.WithStack(err)
	}
	var ms []migration
	for _, name := range names {
		base := filepath.Base(name)
		prefix, _, ok := strings.Cut(base, "_")
		version, err := strconv.Atoi(prefix)
		if !ok || err != nil {
			return nil, errors.WithMessagef(fault.ErrInvalidValue, "migration %s: want NNNN_name.sql", base)
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, errors.WithStack(err)
		}
		ms = append(ms, migration{version: version, name: base, sql: string(data)})
	}
	slices.SortFunc(ms, func(a, b migration) int { return a.version - b.version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, errors.WithMessagef(fault.ErrInvalidValue, "migration %s: want version %d", m.name, i+1)
		}
	}
	return ms, nil
}

// migrate applies the migrations newer than the database's user_version,
// each in a transaction with the version bump.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	ms, err := loadMigrations(fsys)
	if err != nil {
		return err
	}
	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return errors.WithMessage(err, "read schema version")
	}
	if current > len(ms) {
		return errors.WithMessagef(fault.ErrBadState,
			"database schema version %d is newer than this Alfred's %d", current, len(ms))
	}
	for _, m := range ms[current:] {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return errors.WithStack(err)
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return errors.WithMessagef(err, "apply %s", m.name)
		}
		// PRAGMA takes no parameters; the version is our own integer.
		if _, err := tx.ExecContext(ctx, "PRAGMA user_version = "+strconv.Itoa(m.version)); err != nil {
			tx.Rollback()
			return errors.WithMessagef(err, "set schema version %d", m.version)
		}
		if err := tx.Commit(); err != nil {
			return errors.WithMessagef(err, "commit %s", m.name)
		}
	}
	return nil
}
