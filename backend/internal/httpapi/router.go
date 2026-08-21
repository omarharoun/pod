package httpapi

import "net/http"

// routes registers every operation in backend/openapi.yaml.
//
// Routes whose milestone has not landed are registered too, answering 501 rather than
// 404, so that a client calling one early is told why (ADR-007). The list shrinks each
// milestone; when it is empty, delete notImplemented and this comment with it.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// ---- M1: implemented ------------------------------------------------------
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("POST /v1/auth/start", s.handleAuthStart)
	mux.HandleFunc("POST /v1/auth/verify", s.handleAuthVerify)
	mux.HandleFunc("POST /v1/auth/refresh", s.handleAuthRefresh)
	mux.HandleFunc("POST /v1/auth/logout", s.requireAuth(s.handleAuthLogout))
	mux.HandleFunc("GET /v1/me", s.requireAuth(s.handleMe))

	// ---- M2: implemented ------------------------------------------------------
	mux.HandleFunc("GET /v1/onboarding/state", s.requireAuth(s.handleOnboardingState))
	mux.HandleFunc("PUT /v1/onboarding/intent", s.requireAuth(s.handlePutIntent))
	mux.HandleFunc("POST /v1/pool", s.requireAuth(s.handleJoinPool))
	mux.HandleFunc("DELETE /v1/pool", s.requireAuth(s.handleLeavePool))
	mux.HandleFunc("GET /v1/pods/current", s.requireAuth(s.handleCurrentPod))
	mux.HandleFunc("GET /v1/pods/{podId}", s.requireAuth(s.handleGetPod))

	// ---- Specified, not yet built ---------------------------------------------
	for _, route := range []string{
		"GET /v1/pods/{podId}/weeks/current",                          // M3
		"GET /v1/pods/{podId}/weeks",                                  // M3
		"POST /v1/pods/{podId}/weeks/{weekId}/proof",                  // M3
		"POST /v1/pods/{podId}/weeks/{weekId}/proof/{proofId}/commit", // M3
		"GET /v1/pods/{podId}/weeks/{weekId}/proof/{proofId}",         // M3
	} {
		mux.HandleFunc(route, s.requireAuth(s.handleNotImplementedYet))
	}

	// Anything else is genuinely not part of the contract.
	mux.HandleFunc("/", s.handleUnknownRoute)
	return mux
}

func (s *Server) handleNotImplementedYet(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, r, notImplementedYet(), nil)
}

func (s *Server) handleUnknownRoute(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, r, notFound(), nil)
}
