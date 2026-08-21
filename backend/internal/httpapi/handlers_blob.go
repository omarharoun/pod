package httpapi

import (
	"io"
	"net/http"
	"strings"

	"github.com/omarharoun/pod/backend/internal/blob"
)

// The development blob routes.
//
// In production these do not exist: signed URLs point at object storage and the bytes
// never reach this service (ADR-008). They are registered only when the local store is
// selected, which config refuses to do outside development (ADR-015).
//
// They are deliberately outside /v1. They are not part of the contract, they take no
// bearer token, and they must not look like an API anyone should build against — the
// signature in the query is the entire authorisation.
func (s *Server) registerBlobRoutes(mux *http.ServeMux) {
	if s.localBlobs == nil {
		return
	}
	mux.HandleFunc("PUT /blobs/{key...}", s.handleBlobPut)
	mux.HandleFunc("GET /blobs/{key...}", s.handleBlobGet)
}

func (s *Server) handleBlobPut(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !s.authoriseBlob(w, r, key, blob.OperationPut) {
		return
	}
	defer func() { _ = r.Body.Close() }()

	if err := s.localBlobs.Put(key, http.MaxBytesReader(w, r.Body, maxBlobBytes)); err != nil {
		s.writeError(w, r, serverError(), err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleBlobGet(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !s.authoriseBlob(w, r, key, blob.OperationGet) {
		return
	}

	reader, err := s.localBlobs.Open(key)
	if err != nil {
		// Gone is gone. A proof past its expiry and a proof that never existed answer
		// identically (CLAUDE.md §2 rule 4).
		s.writeError(w, r, notFound(), err)
		return
	}
	defer func() { _ = reader.Close() }()

	// Proof is ephemeral and pod-only; nothing about it may be cached or indexed.
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "attachment")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, reader); err != nil {
		s.log.ErrorContext(r.Context(), "blob read failed midway")
	}
}

// maxBlobBytes bounds an upload independently of what the slot said, so a client that
// signs for a small photo cannot then send a large one.
const maxBlobBytes = 12 << 20

func (s *Server) authoriseBlob(w http.ResponseWriter, r *http.Request, key, operation string) bool {
	if !blob.SafeKey(key) || strings.Contains(key, "\\") {
		s.writeError(w, r, notFound(), nil)
		return false
	}
	values := r.URL.Query()
	if values.Get("op") != operation || !s.localBlobs.VerifySignature(key, values, s.clock.Now()) {
		// A forged, expired or retargeted link is a 404, not a 403: there is nothing here
		// to confirm the existence of.
		s.writeError(w, r, notFound(), nil)
		return false
	}
	return true
}
