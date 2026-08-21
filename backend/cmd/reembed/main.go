// Command reembed recomputes every stored goal vector with the current embedder.
//
// A stored embedding is the output of whatever `internal/embed` was on the day it was
// saved. Change the lexicon, the weights or the width, and every vector in the table is
// stale — and `embed.Similarity` answers 0 for a stale width, so the 0.40 signal quietly
// drops out rather than going wrong. That is the right failure, and this is how it ends.
//
// Run it after any change to `internal/embed`. It is idempotent: running it on vectors
// that are already current rewrites them with the same numbers.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/omarharoun/pod/backend/internal/config"
	"github.com/omarharoun/pod/backend/internal/embed"
	"github.com/omarharoun/pod/backend/internal/store"
)

func main() {
	pageSize := flag.Int("page", 200, "how many intents to read at a time")
	dryRun := flag.Bool("dry-run", false, "report what would change without writing")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config", "error", err)
		os.Exit(1)
	}

	// Interruptible: a backfill is safe to stop and safe to restart, and the operator
	// finding that out by pressing ctrl-C beats them wondering.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	started := time.Now()
	read, written, err := reembed(ctx, db, embed.Lexical{}, *pageSize, *dryRun, logger)
	logger.Info("finished",
		"read", read, "written", written, "dry_run", *dryRun,
		"dimensions", embed.Dimensions, "took", time.Since(started).Round(time.Millisecond))
	if err != nil {
		logger.Error("reembed", "error", err)
		os.Exit(1)
	}
}

func reembed(
	ctx context.Context,
	db store.Store,
	embedder embed.Embedder,
	pageSize int,
	dryRun bool,
	logger *slog.Logger,
) (read, written int, err error) {
	var cursor store.IntentKey
	for {
		if err := ctx.Err(); err != nil {
			// Not a failure. Everything written so far is correct and the next run picks
			// up where this one stopped, because the cursor is the data.
			logger.Info("interrupted", "read", read, "written", written)
			return read, written, nil
		}

		page, err := db.ListIntents(ctx, cursor, pageSize)
		if err != nil {
			return read, written, fmt.Errorf("list intents after %+v: %w", cursor, err)
		}
		if len(page) == 0 {
			return read, written, nil
		}

		goals := make([]embed.Goal, len(page))
		for i, intent := range page {
			goals[i] = embed.Goal{Text: intent.GoalText, Category: intent.GoalCategory}
		}
		vectors, err := embedder.Embed(ctx, goals)
		if err != nil {
			return read, written, fmt.Errorf("embed page after %+v: %w", cursor, err)
		}
		if len(vectors) != len(page) {
			return read, written, fmt.Errorf("embedder returned %d vectors for %d goals", len(vectors), len(page))
		}

		for i, intent := range page {
			read++
			cursor = store.IntentKey{UserID: intent.UserID, Slot: intent.Slot}
			if same(intent.Embedding, vectors[i]) {
				continue
			}
			written++
			if dryRun {
				continue
			}
			if err := db.ReplaceEmbedding(ctx, cursor, vectors[i]); err != nil {
				return read, written, fmt.Errorf("replace embedding for %+v: %w", cursor, err)
			}
		}
	}
}

func same(stored []float32, fresh embed.Vector) bool {
	if len(stored) != len(fresh) {
		return false
	}
	for i := range stored {
		if stored[i] != fresh[i] {
			return false
		}
	}
	return true
}
