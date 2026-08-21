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

	// ---- M4: implemented ------------------------------------------------------
	mux.HandleFunc("PUT /v1/devices", s.requireAuth(s.handleRegisterDevice))
	mux.HandleFunc("DELETE /v1/devices", s.requireAuth(s.handleUnregisterDevice))

	// ---- M2: implemented ------------------------------------------------------
	mux.HandleFunc("GET /v1/onboarding/state", s.requireAuth(s.handleOnboardingState))
	mux.HandleFunc("GET /v1/onboarding/intent", s.requireAuth(s.handleGetIntent))
	mux.HandleFunc("PUT /v1/onboarding/intent", s.requireAuth(s.handlePutIntent))
	mux.HandleFunc("POST /v1/pool", s.requireAuth(s.handleJoinPool))
	mux.HandleFunc("DELETE /v1/pool", s.requireAuth(s.handleLeavePool))
	mux.HandleFunc("GET /v1/pods/current", s.requireAuth(s.handleCurrentPod))
	mux.HandleFunc("GET /v1/pods/{podId}", s.requireAuth(s.handleGetPod))

	// ---- M3: implemented ------------------------------------------------------
	mux.HandleFunc("GET /v1/pods/{podId}/weeks/current", s.requireAuth(s.handleCurrentWeek))
	mux.HandleFunc("GET /v1/pods/{podId}/weeks", s.requireAuth(s.handleListWeeks))
	mux.HandleFunc("PUT /v1/pods/{podId}/weeks/{weekId}/action", s.requireAuth(s.handleSetWeekAction))
	mux.HandleFunc("POST /v1/pods/{podId}/weeks/{weekId}/proof", s.requireAuth(s.handleCreateProofUpload))
	mux.HandleFunc("POST /v1/pods/{podId}/weeks/{weekId}/proof/{proofId}/commit", s.requireAuth(s.handleCommitProof))
	mux.HandleFunc("GET /v1/pods/{podId}/weeks/{weekId}/proof/{proofId}", s.requireAuth(s.handleGetProof))

	// ---- M7: implemented ------------------------------------------------------
	mux.HandleFunc("GET /v1/slots", s.requireAuth(s.handleListSlots))
	mux.HandleFunc("POST /v1/slots", s.requireAuth(s.handleClaimSlot))

	// ---- M6: implemented ------------------------------------------------------
	mux.HandleFunc("POST /v1/pods/{podId}/continue", s.requireAuth(s.handleContinueCycle))

	// Every operation in the contract is now implemented, so the not-yet-built table
	// from ADR-007 is empty and gone. handleNotImplementedYet stays for the next
	// milestone that specifies ahead of building.

	s.registerBlobRoutes(mux)

	// Anything else is genuinely not part of the contract.
	mux.HandleFunc("/", s.handleUnknownRoute)
	return mux
}

func (s *Server) handleUnknownRoute(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, r, notFound(), nil)
}
