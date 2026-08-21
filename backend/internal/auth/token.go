package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/omarharoun/pod/backend/internal/clock"
)

// ErrInvalidToken covers every way an access token can fail to be usable. It is
// deliberately undifferentiated: the client is told to sign in again either way, and a
// more specific error only helps an attacker.
var ErrInvalidToken = errors.New("auth: invalid access token")

// AccessTokens issues and verifies the short-lived bearer token.
//
// HS256 by hand rather than via a JWT library: the service is both issuer and verifier,
// there is exactly one algorithm, and the alternative — accepting an `alg` from the token
// itself — is the classic way this goes wrong. The header is written, not parsed for
// dispatch.
type AccessTokens struct {
	secret []byte
	ttl    time.Duration
	clock  clock.Clock
	issuer string
}

func NewAccessTokens(secret string, ttl time.Duration, c clock.Clock) *AccessTokens {
	return &AccessTokens{secret: []byte(secret), ttl: ttl, clock: c, issuer: "pod"}
}

type claims struct {
	Sub string `json:"sub"`
	Iss string `json:"iss"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

const fixedHeader = `{"alg":"HS256","typ":"JWT"}`

// Issue returns a signed token for userID and the moment it stops being valid.
func (a *AccessTokens) Issue(userID string) (string, time.Time, error) {
	now := a.clock.Now()
	expires := now.Add(a.ttl)
	payload, err := json.Marshal(claims{Sub: userID, Iss: a.issuer, Iat: now.Unix(), Exp: expires.Unix()})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: encode claims: %w", err)
	}
	signing := encode([]byte(fixedHeader)) + "." + encode(payload)
	return signing + "." + encode(a.sign(signing)), expires, nil
}

// Verify returns the subject of a valid, unexpired token.
func (a *AccessTokens) Verify(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ErrInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", ErrInvalidToken
	}
	if !hmac.Equal(signature, a.sign(parts[0]+"."+parts[1])) {
		return "", ErrInvalidToken
	}
	// Only now that the signature holds is any of the content worth reading.
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || string(header) != fixedHeader {
		return "", ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ErrInvalidToken
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", ErrInvalidToken
	}
	if c.Iss != a.issuer || c.Sub == "" {
		return "", ErrInvalidToken
	}
	if a.clock.Now().Unix() >= c.Exp {
		return "", ErrInvalidToken
	}
	return c.Sub, nil
}

func (a *AccessTokens) sign(signing string) []byte {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write([]byte(signing))
	return mac.Sum(nil)
}

func encode(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// hashRefreshToken is what the database stores. The token itself is high-entropy, so a
// plain SHA-256 is enough — there is nothing to brute-force.
func hashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
