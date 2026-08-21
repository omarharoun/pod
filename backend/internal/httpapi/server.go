// Package httpapi serves backend/openapi.yaml. Handlers map between the service layer
// and the generated wire types in internal/api; they contain no product logic of their
// own, and no user-facing sentences (ADR-003).
package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/omarharoun/pod/backend/internal/auth"
	"github.com/omarharoun/pod/backend/internal/clock"
	"github.com/omarharoun/pod/backend/internal/config"
	"github.com/omarharoun/pod/backend/internal/store"
)

type Server struct {
	auth  *auth.Service
	store store.Store
	clock clock.Clock
	cfg   config.Config
	log   *slog.Logger

	handler http.Handler
}

func New(authService *auth.Service, s store.Store, c clock.Clock, cfg config.Config, log *slog.Logger) *Server {
	srv := &Server{auth: authService, store: s, clock: c, cfg: cfg, log: log}
	srv.handler = srv.withRecovery(srv.withRequestID(srv.withLogging(srv.routes())))
	return srv
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}
