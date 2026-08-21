// Command matcher runs the matching window.
//
//	go run ./cmd/matcher once   # one window, then exit — for cron, and for a local look
//	go run ./cmd/matcher serve  # run every hour until interrupted
//
// CLAUDE.md §5: matching is a scheduled job, never an on-request operation. Someone
// entering the pool waits for the next window, and that is the intended experience —
// instant matching would pick from whoever happened to be queued that second.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/config"
	"github.com/omarharoun/pod/backend/internal/matching"
	"github.com/omarharoun/pod/backend/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("fatal", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	command := "once"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command != "once" && command != "serve" {
		return fmt.Errorf("matcher: unknown command %q (want `once` or `serve`)", command)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	job := matching.NewJob(db, clock.System{}, log, matching.Options{})

	if command == "once" {
		_, err := job.RunWindow(ctx)
		return err
	}

	log.Info("matching every window", slog.Duration("interval", matching.WindowInterval))
	ticker := time.NewTicker(matching.WindowInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("matcher stopping")
			return nil
		case <-ticker.C:
			if _, err := job.RunWindow(ctx); err != nil {
				// A failed window is not a reason to stop matching forever; the pool is
				// still there and the next window will try again.
				log.Error("matching window failed", slog.Any("error", err))
			}
		}
	}
}
