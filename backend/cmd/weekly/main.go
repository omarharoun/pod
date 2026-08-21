// Command weekly turns pods' weeks over and sweeps expired proof.
//
//	go run ./cmd/weekly once    # one tick, then exit — for cron, and for a local look
//	go run ./cmd/weekly serve   # every hour until interrupted
//
// It runs hourly because weeks turn over at local midnight in every timezone the product
// has a pod in, and hourly is the coarsest cadence that never leaves a pod a whole day
// behind its own boundary (CLAUDE.md §5).
//
// The sweep is CLAUDE.md §2 rule 4 being kept: proof is deleted at cycle end, server-side.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/omarharoun/pod/backend/internal/blob"
	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/config"
	"github.com/omarharoun/pod/backend/internal/store"
	"github.com/omarharoun/pod/backend/internal/week"
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
		return fmt.Errorf("weekly: unknown command %q (want `once` or `serve`)", command)
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

	blobs, err := selectBlobStore(cfg, log)
	if err != nil {
		return err
	}

	now := clock.System{}
	job := week.NewJob(week.NewService(db, blobs, now), db, blobs, now, log)

	if command == "once" {
		_, err := job.Tick(ctx)
		return err
	}

	log.Info("turning weeks over", slog.Duration("interval", week.TickInterval))
	ticker := time.NewTicker(week.TickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("weekly stopping")
			return nil
		case <-ticker.C:
			if _, err := job.Tick(ctx); err != nil {
				// A failed tick is not a reason to stop turning weeks over forever.
				log.Error("week tick failed", slog.Any("error", err))
			}
		}
	}
}

// selectBlobStore mirrors the API's: it refuses to run outside development without object
// storage, because a sweep that cannot reach the bytes cannot keep the promise.
func selectBlobStore(cfg config.Config, log *slog.Logger) (blob.Store, error) {
	if !cfg.IsDevelopment() {
		return nil, fmt.Errorf("config: no object storage is configured for env %q; wire one in before deploying (ADR-008)", cfg.Env)
	}
	local, err := blob.NewLocal(cfg.BlobRoot, cfg.PublicURL, cfg.AuthSecret)
	if err != nil {
		return nil, err
	}
	log.Warn("development blob store selected — sweeping proof from this machine's disk",
		slog.String("root", cfg.BlobRoot))
	return local, nil
}
