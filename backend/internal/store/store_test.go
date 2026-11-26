package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"gitlab.com/tozd/go/errors"

	"github.com/sundbassen/alfred/internal/platform/fault"
)

func open(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "alfred.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func TestOpenMigratesAndReopens(t *testing.T) {
	db, path := open(t)
	ms, err := loadMigrations(migrations)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != len(ms) {
		t.Fatalf("user_version = %d, %v; want %d", version, err, len(ms))
	}
	if _, err := db.Exec("INSERT INTO timers (kind, at, duration, message) VALUES ('timer', 1, 2, '')"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Reopening applies nothing and keeps the data.
	db, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM timers").Scan(&n); err != nil || n != 1 {
		t.Errorf("rows after reopen = %d, %v", n, err)
	}
}

func TestFilePermissions(t *testing.T) {
	_, path := open(t)
	for p, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", p, got, want)
		}
	}
}

func TestPragmasOnEveryConnection(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	// Hold two connections at once so the pool opens a second one.
	for range 2 {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var fk int
		var mode string
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
			t.Errorf("foreign_keys = %d, %v", fk, err)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
			t.Errorf("journal_mode = %q, %v", mode, err)
		}
	}
}

func TestEveryTableIsStrict(t *testing.T) {
	db, _ := open(t)
	rows, err := db.Query("SELECT name, sql FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(strings.ToUpper(strings.TrimSpace(def)), "STRICT") {
			t.Errorf("table %s is not STRICT:\n%s", name, def)
		}
	}
	// And STRICT is enforced: text in an INTEGER column fails.
	if _, err := db.Exec("INSERT INTO timers (kind, at, duration, message) VALUES ('timer', 'soon', 0, '')"); err == nil {
		t.Error("STRICT table accepted text in an INTEGER column")
	}
}

func TestMigrationErrors(t *testing.T) {
	ctx := context.Background()
	gap := fstest.MapFS{
		"migrations/0001_a.sql": {Data: []byte("CREATE TABLE a (x INTEGER) STRICT;")},
		"migrations/0003_c.sql": {Data: []byte("CREATE TABLE c (x INTEGER) STRICT;")},
	}
	if _, err := loadMigrations(gap); !errors.Is(err, fault.ErrInvalidValue) {
		t.Errorf("gap: %v, want ErrInvalidValue", err)
	}

	db, _ := open(t)
	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, migrations); !errors.Is(err, fault.ErrBadState) {
		t.Errorf("newer database: %v, want ErrBadState", err)
	}

	// A failing migration rolls back, leaving the version where it was:
	// after the real ones (open applied them), one more that fails.
	db2, _ := open(t)
	real, err := loadMigrations(migrations)
	if err != nil {
		t.Fatal(err)
	}
	n := len(real)
	bad := fstest.MapFS{}
	for i := 1; i <= n; i++ {
		bad[fmt.Sprintf("migrations/%04d_m.sql", i)] = &fstest.MapFile{Data: []byte("SELECT 1;")}
	}
	bad[fmt.Sprintf("migrations/%04d_bad.sql", n+1)] = &fstest.MapFile{Data: []byte("CREATE TABLE b (x INTEGER) STRICT; SELECT nope FROM missing;")}
	if err := migrate(ctx, db2, bad); err == nil {
		t.Fatal("bad migration succeeded")
	}
	var version int
	db2.QueryRow("PRAGMA user_version").Scan(&version)
	var tables int
	db2.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name = 'b'").Scan(&tables)
	if version != n || tables != 0 {
		t.Errorf("after a failed migration: version %d, table b exists %d; want %d and 0", version, tables, n)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	if got := DefaultPath(); got != "/state/alfred/alfred.db" {
		t.Errorf("DefaultPath = %q", got)
	}
}

func TestTimersKeepTheirRowsWhenIDsBecomeAutoincrement(t *testing.T) {
	// A database from before 0002 keeps its timers, and ids then never come
	// back after a delete.
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, err := fs.ReadFile(migrations, "migrations/0001_timers.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, fstest.MapFS{"migrations/0001_timers.sql": {Data: first}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO timers (id, kind, at, duration, message) VALUES (7, 'timer', 1, 60000, '')"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.QueryRow("SELECT id FROM timers").Scan(&id); err != nil || id != 7 {
		t.Fatalf("after migrating: id %d, %v; want the timer with id 7", id, err)
	}
	if _, err := db.Exec("DELETE FROM timers"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("INSERT INTO timers (kind, at, duration, message) VALUES ('timer', 1, 60000, '') RETURNING id").Scan(&id); err != nil || id != 8 {
		t.Errorf("new id after a delete: %d, %v; want 8", id, err)
	}
}
