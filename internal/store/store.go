// Package store owns the SQLite database: connection settings, schema migrations,
// and the query helpers the rest of verdande builds on.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, so the binary stays static
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type DB struct {
	*sql.DB
	path string
}

// dsn is the connection string.
//
// SQLite is a single file, but it is being driven by an HTTP server with concurrent
// readers and a WebSocket fan-out, so the pragmas below matter more than usual:
// WAL lets readers run while a write is in flight, busy_timeout turns the "database
// is locked" race into a short wait, and foreign_keys is off by default in SQLite
// and has to be asked for on every connection.
//
// Its own function so the migration tests can open a database with exactly the
// pragmas production runs with.
func dsn(path string) string {
	return "file:" + filepath.ToSlash(path) + "?" + strings.Join([]string{
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=foreign_keys(ON)",
		"_pragma=synchronous(NORMAL)",
		// Every write transaction takes the write lock up front.
		//
		// Without this, `BEGIN` is deferred: a transaction starts as a reader and
		// asks for the write lock at its first UPDATE. SQLite refuses that upgrade
		// with SQLITE_BUSY *immediately* and does not apply busy_timeout to it —
		// backing off cannot help, because the transaction is already holding a
		// read snapshot that the other writer is invalidating. So the timeout above
		// looks like it covers writer contention and covers none of it: two people
		// saving at the same moment get a 500 in two milliseconds.
		//
		// `immediate` takes the lock at BEGIN instead, where busy_timeout does
		// apply and the second writer waits its turn. The driver leaves read-only
		// transactions deferred, so readers still run concurrently.
		"_txlock=immediate",
		// Keep the temp b-trees FTS5 and ORDER BY build in memory rather than
		// spilling them into the data volume.
		"_pragma=temp_store(MEMORY)",
	}, "&")
}

// Open prepares the database at path and brings the schema up to date.
func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite serialises writes whatever the pool does, so this is sized for readers.
	// In WAL mode they run concurrently with a write in flight, and busy_timeout
	// absorbs the contention between writers.
	//
	// Not 1. A single connection makes the pool a lock, and any code that runs a
	// query while an earlier *sql.Rows is still open then waits forever for a
	// connection it is itself holding — a deadlock rather than a slow query, and
	// one that only appears once a list has rows in it. The store closes its result
	// sets before querying again, and this leaves room for the mistake to be slow
	// instead of fatal if it is ever made again.
	sqlDB.SetMaxOpenConns(max(4, runtime.NumCPU()))
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := sqlDB.PingContext(context.Background()); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	db := &DB{DB: sqlDB, path: path}
	if err := db.migrate(context.Background()); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Path() string { return db.path }

// rebuildMarker opts a migration out of foreign key enforcement for the length of
// its transaction.
//
// SQLite cannot alter a column's foreign key action, so changing one means
// rebuilding the table: copy it, drop the original, rename the copy back. With
// foreign keys on, the drop is a cascading delete — every row in every table that
// references `tasks` would go with it, which is the opposite of what a migration
// that exists to *stop* losing rows should do. `PRAGMA foreign_keys` is a no-op
// inside a transaction, so it has to be set on the connection before BEGIN; that
// is why migrations run on a connection of their own rather than on the pool.
//
// `PRAGMA foreign_key_check` runs before the commit, so a rebuild that leaves a
// dangling reference fails the migration instead of writing it.
const rebuildMarker = "-- verdande:rebuild-tables"

// migrate applies every embedded migration that has not run yet, in filename order,
// each inside its own transaction. A migration that fails leaves the schema at the
// last version that succeeded rather than half-applied.
//
// Everything here runs on one connection: a migration marked with rebuildMarker
// needs a pragma to hold across its transaction, and a pragma only applies to the
// connection it was issued on.
func (db *DB) migrate(ctx context.Context) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		applied[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	entries, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)

	for _, entry := range entries {
		name := filepath.Base(entry)
		if applied[name] {
			continue
		}
		body, err := migrationFS.ReadFile(entry)
		if err != nil {
			return err
		}
		if err := db.applyMigration(ctx, conn, name, string(body)); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) applyMigration(ctx context.Context, conn *sql.Conn, name, body string) error {
	if strings.Contains(body, rebuildMarker) {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return fmt.Errorf("migration %s: disable foreign keys: %w", name, err)
		}
		// Back on whatever happens below: this connection goes back to the pool.
		defer conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, body); err != nil {
		tx.Rollback()
		return fmt.Errorf("migration %s: %w", name, err)
	}
	if strings.Contains(body, rebuildMarker) {
		if err := foreignKeyCheck(ctx, tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (name, applied_at) VALUES (?, unixepoch())`, name); err != nil {
		tx.Rollback()
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

// foreignKeyCheck reports the first row that references something that is not
// there. It names the table and the rowid, because a rebuild that drops rows on the
// floor is otherwise indistinguishable from one that worked.
func foreignKeyCheck(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("foreign key check: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return fmt.Errorf("foreign key check: %w", err)
		}
		return fmt.Errorf("foreign key check: %s row %d references a missing %s",
			table, rowid.Int64, parent)
	}
	return rows.Err()
}

// Tx runs fn inside a transaction, rolling back on error or panic.
func (db *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
