package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/omarharoun/pod/backend/internal/ids"
)

type contextKey int

const (
	contextKeyRequestID contextKey = iota
	contextKeyUserID
)

// RequestIDFrom returns the id assigned to this request, which is echoed in every error
// body so a bug report can be traced to a log line.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(contextKeyRequestID).(string)
	return id
}

// UserIDFrom returns the authenticated caller. It is only populated behind requireAuth.
func UserIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(contextKeyUserID).(string)
	return id
}

func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" || len(id) > 64 {
			generated, err := ids.NewUUID()
			if err != nil {
				generated = "unknown"
			}
			id = generated
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKeyRequestID, id)))
	})
}

// statusRecorder remembers the status so the access log can report it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := s.clock.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.log.InfoContext(r.Context(), "request",
			slog.String("request_id", RequestIDFrom(r.Context())),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", recorder.status),
			slog.Duration("took", time.Since(started.Local())),
		)
	})
}

// withRecovery turns a panic into a 500 rather than a dropped connection. A panic is
// always a bug; this exists so one request's bug is not every request's outage.
func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.log.ErrorContext(r.Context(), "panic serving request",
					slog.String("request_id", RequestIDFrom(r.Context())),
					slog.Any("panic", recovered),
					slog.String("stack", stack()),
				)
				s.writeError(w, r, serverError(), nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requireAuth rejects anything without a usable bearer token, and puts the caller's id
// in the context for the handler.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		scheme, token, found := strings.Cut(header, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
			s.writeError(w, r, unauthorized(), nil)
			return
		}
		userID, err := s.auth.AccessTokens().Verify(strings.TrimSpace(token))
		if err != nil {
			s.writeError(w, r, unauthorized(), err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), contextKeyUserID, userID)))
	}
}
