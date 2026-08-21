package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/omarharoun/pod/backend/internal/api"
)

const healthCheckTimeout = 2 * time.Second

// handleHealth reports whether the service can serve. Postgres is the source of truth,
// so if it is unreachable the answer is `down` and the status is 503 — a green health
// check that lies is worse than no health check.
//
// Redis joins this list in M2, when the matching queue exists. It will be `degraded`
// rather than `down`: a queue outage delays matching, it does not stop the week loop.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
	defer cancel()

	check := api.HealthCheck{Name: "postgres", Status: "ok"}
	started := s.clock.Now()
	if err := s.store.Ping(ctx); err != nil {
		check.Status = "down"
		detail := err.Error()
		check.Detail = &detail // operator-facing only; never rendered to a user
	}
	latency := int(time.Since(started.Local()).Milliseconds())
	check.LatencyMs = &latency

	body := api.Health{Status: check.Status, Version: s.cfg.Version, Checks: []api.HealthCheck{check}}
	status := http.StatusOK
	if body.Status == "down" {
		status = http.StatusServiceUnavailable
	}
	s.writeJSON(w, r, status, body)
}
