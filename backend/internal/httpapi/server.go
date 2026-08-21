// Package httpapi serves backend/openapi.yaml. Handlers map between the service layer
// and the generated wire types in internal/api; they contain no product logic of their
// own, and no user-facing sentences (ADR-003).
package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/omarharoun/pod/backend/internal/auth"
	"github.com/omarharoun/pod/backend/internal/blob"
	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/config"
	"github.com/omarharoun/pod/backend/internal/onboarding"
	"github.com/omarharoun/pod/backend/internal/store"
	"github.com/omarharoun/pod/backend/internal/week"
)

type Server struct {
	auth       *auth.Service
	onboarding *onboarding.Service
	weeks      *week.Service
	store      store.Store
	clock      clock.Clock
	cfg        config.Config
	log        *slog.Logger

	// localBlobs is set only when the development blob store is in use, and is what
	// registers the /blobs routes. In production the bytes never reach this service, so
	// it is nil and those routes do not exist (ADR-008, ADR-015).
	localBlobs *blob.Local

	handler http.Handler
}

func New(
	authService *auth.Service,
	onboardingService *onboarding.Service,
	weekService *week.Service,
	s store.Store,
	c clock.Clock,
	cfg config.Config,
	log *slog.Logger,
	localBlobs *blob.Local,
) *Server {
	srv := &Server{
		auth: authService, onboarding: onboardingService, weeks: weekService,
		store: s, clock: c, cfg: cfg, log: log, localBlobs: localBlobs,
	}
	srv.handler = srv.withRecovery(srv.withRequestID(srv.withLogging(srv.routes())))
	return srv
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}
