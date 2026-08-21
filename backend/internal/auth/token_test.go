package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/omarharoun/pod/backend/internal/clock"
)

func newTokens(t *testing.T) (*AccessTokens, *clock.Fixed) {
	t.Helper()
	c := clock.NewFixed(time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC))
	return NewAccessTokens("test-secret-that-is-at-least-32-bytes", 15*time.Minute, c), c
}

func TestIssueAndVerifyRoundTrip(t *testing.T) {
	tokens, c := newTokens(t)
	token, expires, err := tokens.Issue("user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !expires.Equal(c.Now().Add(15 * time.Minute)) {
		t.Errorf("expiry: got %v", expires)
	}
	subject, err := tokens.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if subject != "user-1" {
		t.Errorf("subject: got %q", subject)
	}
}

func TestVerifyRejectsExpiredTokens(t *testing.T) {
	tokens, c := newTokens(t)
	token, _, err := tokens.Issue("user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	c.Advance(15*time.Minute + time.Second)
	if _, err := tokens.Verify(token); err == nil {
		t.Fatal("expected an expired token to be rejected")
	}
}

func TestVerifyRejectsATokenSignedWithAnotherKey(t *testing.T) {
	tokens, c := newTokens(t)
	other := NewAccessTokens("a-completely-different-secret-value-x", 15*time.Minute, c)
	token, _, err := other.Issue("user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := tokens.Verify(token); err == nil {
		t.Fatal("a token signed with another key must not verify")
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	tokens, _ := newTokens(t)
	token, _, err := tokens.Issue("user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	parts := strings.Split(token, ".")
	forged, err := json.Marshal(claims{Sub: "user-2", Iss: "pod", Iat: 0, Exp: 1 << 40})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(forged) + "." + parts[2]
	if _, err := tokens.Verify(tampered); err == nil {
		t.Fatal("a re-signed-less payload swap must not verify")
	}
}

// The classic JWT failure: a token that declares `alg: none` (or any other algorithm)
// and is verified with whatever it asked for.
func TestVerifyRejectsAlgNone(t *testing.T) {
	tokens, _ := newTokens(t)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, err := json.Marshal(claims{Sub: "user-1", Iss: "pod", Exp: 1 << 40})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(payload) + "."
	if _, err := tokens.Verify(unsigned); err == nil {
		t.Fatal("alg:none must not verify")
	}
}

func TestVerifyRejectsMalformedTokens(t *testing.T) {
	tokens, _ := newTokens(t)
	for _, token := range []string{"", "a", "a.b", "a.b.c.d", "...", "not base64.at all.here"} {
		if _, err := tokens.Verify(token); err == nil {
			t.Errorf("Verify(%q) should have failed", token)
		}
	}
}

func TestLoginCodesAreSixDigitsAndSpreadOut(t *testing.T) {
	seen := map[string]int{}
	for i := 0; i < 2000; i++ {
		code, err := newLoginCode()
		if err != nil {
			t.Fatalf("newLoginCode: %v", err)
		}
		if len(code) != 6 {
			t.Fatalf("got %q, want six characters", code)
		}
		if strings.Trim(code, "0123456789") != "" {
			t.Fatalf("got %q, want digits only", code)
		}
		seen[code]++
	}
	// 2000 draws from a million values: a handful of collisions is expected, a pile of
	// them means the generator is not what we think it is.
	if len(seen) < 1990 {
		t.Fatalf("only %d distinct codes in 2000 draws — the distribution looks wrong", len(seen))
	}
}

func TestCodeHashIsBoundToItsChallenge(t *testing.T) {
	const secret, code = "s3cret-value-for-this-test-only-ok", "123456"
	a := hashLoginCode(secret, "challenge-a", code)
	b := hashLoginCode(secret, "challenge-b", code)
	if a == b {
		t.Fatal("the same code hashes identically across challenges — a hash could be replayed")
	}
	if !codeMatches(secret, "challenge-a", code, a) {
		t.Fatal("the code should match its own challenge")
	}
	if codeMatches(secret, "challenge-b", code, a) {
		t.Fatal("a hash from another challenge must not match")
	}
	if codeMatches("a-different-server-secret-entirely!", "challenge-a", code, a) {
		t.Fatal("a different pepper must not match — a stolen row would be a credential")
	}
}

func TestLooksLikeCode(t *testing.T) {
	for _, good := range []string{"000000", "123456", "999999"} {
		if !looksLikeCode(good) {
			t.Errorf("looksLikeCode(%q) = false", good)
		}
	}
	for _, bad := range []string{"", "12345", "1234567", "12345a", " 12345", "12 456"} {
		if looksLikeCode(bad) {
			t.Errorf("looksLikeCode(%q) = true", bad)
		}
	}
}
