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
	"sort"
	"strings"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, so the binary stays static
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type DB struct {
	*sql.DB
	path string
}

// Open prepares the database at path and brings the schema up to date.
//
// SQLite is a single file, but it is being driven by an HTTP server with concurrent
// readers and a WebSocket fan-out, so the pragmas below matter more than usual:
// WAL lets readers run while a write is in flight, busy_timeout turns the "database
// is locked" race into a short wait, and foreign_keys is off by default in SQLite
// and has to be asked for on every connection.
func Open(path string) (*DB, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?" + strings.Join([]string{
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=foreign_keys(ON)",
		"_pragma=synchronous(NORMAL)",
		// Keep the temp b-trees FTS5 and ORDER BY build in memory rather than
		// spilling them into the data volume.
		"_pragma=temp_store(MEMORY)",
	}, "&")

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// One writer. SQLite serialises writes anyway, and letting the pool open several
	// write connections only converts that into SQLITE_BUSY churn. Reads still run
	// concurrently against the WAL through this same pool.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

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

// migrate applies every embedded migration that has not run yet, in filename order,
// each inside its own transaction. A migration that fails leaves the schema at the
// last version that succeeded rather than half-applied.
func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT name FROM schema_migrations`)
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

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (name, applied_at) VALUES (?, unixepoch())`, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
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
