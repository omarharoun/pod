package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// codeLength is fixed at 6 across the API (`AuthStartResponse.code_length`) and the
// copy ("6-digit code"). Changing it means changing both.
const codeLength = 6

// newLoginCode returns a uniformly random 6-digit code, leading zeros included.
//
// crypto/rand rather than math/rand: this is a credential, and a predictable one turns
// the whole passwordless design into an open door.
func newLoginCode() (string, error) {
	limit := big.NewInt(1)
	for i := 0; i < codeLength; i++ {
		limit.Mul(limit, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", fmt.Errorf("auth: generate code: %w", err)
	}
	return fmt.Sprintf("%0*d", codeLength, n), nil
}

// hashLoginCode peppers the code with the service secret and binds it to its challenge,
// so a stolen database row is not a credential and a hash cannot be replayed against a
// different challenge (ADR-002).
func hashLoginCode(secret, challengeID, code string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(challengeID))
	mac.Write([]byte{0})
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

func codeMatches(secret, challengeID, code, storedHash string) bool {
	return hmac.Equal([]byte(hashLoginCode(secret, challengeID, code)), []byte(storedHash))
}

// looksLikeCode is a shape check only. It exists so an obviously malformed body is a 400
// rather than a wasted attempt against the challenge's counter.
func looksLikeCode(code string) bool {
	if len(code) != codeLength {
		return false
	}
	return strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) < 0
}
