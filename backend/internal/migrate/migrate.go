// Package migrate applies the SQL migrations in backend/migrations.
//
// It is deliberately the simplest thing that is safe: numbered files, applied once, in
// order, each inside a transaction, recorded in a table. No down-migrations — reversing
// a schema change in production is a new migration, written with the data in front of
// you, not one written months earlier in the dark.
package migrate

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed all:sql
var files embed.FS

type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Load returns the embedded migrations in version order.
func Load() ([]Migration, error) {
	entries, err := fs.ReadDir(files, "sql")
	if err != nil {
		return nil, fmt.Errorf("migrate: read migrations: %w", err)
	}
	var out []Migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, found := strings.Cut(entry.Name(), "_")
		if !found {
			return nil, fmt.Errorf("migrate: %s: expected a NNNN_name.sql filename", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migrate: %s: %w", entry.Name(), err)
		}
		body, err := fs.ReadFile(files, "sql/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("migrate: %s: %w", entry.Name(), err)
		}
		out = append(out, Migration{Version: version, Name: entry.Name(), SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migrate: expected version %d, found %d (%s) — versions must be contiguous", i+1, m.Version, m.Name)
		}
	}
	return out, nil
}

// Up applies every migration the database has not seen, and returns the ones it applied.
func Up(ctx context.Context, conn *pgx.Conn) ([]Migration, error) {
	migrations, err := Load()
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    integer PRIMARY KEY,
			name       text        NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return nil, fmt.Errorf("migrate: create schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("migrate: read schema_migrations: %w", err)
	}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return nil, fmt.Errorf("migrate: scan version: %w", err)
		}
		applied[version] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate: read schema_migrations: %w", err)
	}

	var ran []Migration
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		if err := apply(ctx, conn, m); err != nil {
			return ran, err
		}
		ran = append(ran, m)
	}
	return ran, nil
}

func apply(ctx context.Context, conn *pgx.Conn, m Migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %s: begin: %w", m.Name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return fmt.Errorf("migrate: %s: %w", m.Name, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.Version, m.Name); err != nil {
		return fmt.Errorf("migrate: %s: record: %w", m.Name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: %s: commit: %w", m.Name, err)
	}
	return nil
}
