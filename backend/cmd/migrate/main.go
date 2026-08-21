// Command migrate applies pending database migrations.
//
//	go run ./cmd/migrate up
//
// It reads DATABASE_URL. There is no `down`: see internal/migrate.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/omarharoun/pod/backend/internal/migrate"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command != "up" {
		return fmt.Errorf("migrate: unknown command %q (only `up` exists — see internal/migrate)", command)
	}

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("migrate: DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return fmt.Errorf("migrate: connect: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	applied, err := migrate.Up(ctx, conn)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Println("migrate: nothing to do")
		return nil
	}
	for _, m := range applied {
		fmt.Printf("migrate: applied %s\n", m.Name)
	}
	return nil
}
