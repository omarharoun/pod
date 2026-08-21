// Command api serves the Pod API described by backend/openapi.yaml.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/omarharoun/pod/backend/internal/auth"
	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/config"
	"github.com/omarharoun/pod/backend/internal/embed"
	"github.com/omarharoun/pod/backend/internal/httpapi"
	"github.com/omarharoun/pod/backend/internal/mail"
	"github.com/omarharoun/pod/backend/internal/onboarding"
	"github.com/omarharoun/pod/backend/internal/store"
)

const shutdownGrace = 15 * time.Second

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("fatal", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
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

	mailer, err := selectMailer(cfg, log)
	if err != nil {
		return err
	}

	now := clock.System{}
	// The hashing embedder until the hosted one is wired in — see ADR-011.
	onboardingService := onboarding.NewService(db, embed.Hashing{}, now)
	server := httpapi.New(auth.NewService(db, mailer, now, cfg), onboardingService, db, now, cfg, log)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	listenErr := make(chan error, 1)
	go func() {
		log.Info("listening", slog.String("addr", cfg.Addr), slog.String("env", cfg.Env), slog.String("version", cfg.Version))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			listenErr <- err
		}
		close(listenErr)
	}()

	select {
	case err := <-listenErr:
		return err
	case <-ctx.Done():
		log.Info("shutting down", slog.Duration("grace", shutdownGrace))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

// selectMailer refuses to start outside development without a real provider, rather than
// silently logging login codes where nobody will read them.
func selectMailer(cfg config.Config, log *slog.Logger) (mail.Mailer, error) {
	if cfg.IsDevelopment() {
		log.Warn("development mailer selected — login codes are written to this log, not sent")
		return mail.Logger{Log: log}, nil
	}
	return nil, fmt.Errorf("config: no transactional email provider is configured for env %q; wire one into selectMailer before deploying", cfg.Env)
}
