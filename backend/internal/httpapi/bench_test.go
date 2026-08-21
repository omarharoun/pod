package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/omarharoun/pod/backend/internal/api"
)

// These measure the language-and-framework cost of a request with the datastore taken
// out of the picture, so the number is "everything Go does" and nothing else.

func BenchmarkAuthenticatedRequest(b *testing.B) {
	h := newHarness(b)
	session := h.signIn("sam@example.com")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		req.Header.Set("Authorization", "Bearer "+session.AccessToken)
		rec := httptest.NewRecorder()
		h.server.ServeHTTP(rec, req)
		if rec.Code != 200 {
			b.Fatalf("status %d", rec.Code)
		}
	}
}

func BenchmarkTokenVerify(b *testing.B) {
	h := newHarness(b)
	session := h.signIn("sam@example.com")
	tokens := h.server.auth.AccessTokens()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tokens.Verify(session.AccessToken); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONResponseEncode(b *testing.B) {
	h := newHarness(b)
	session := h.signIn("sam@example.com")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		h.server.writeJSON(rec, httptest.NewRequest(http.MethodGet, "/v1/me", nil), 200, session.User)
	}
}

func BenchmarkRequestBodyDecode(b *testing.B) {
	body := `{"challenge_id":"1a2b3c4d-1111-4111-8111-111111111111","code":"482913"}`

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var dst api.AuthVerifyRequest
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/verify", strings.NewReader(body))
		if err := decodeJSON(req, &dst); err != nil {
			b.Fatal(err)
		}
	}
}
