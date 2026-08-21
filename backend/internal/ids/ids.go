// Package ids generates the identifiers this service hands out.
package ids

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// NewUUID returns a random (version 4) UUID in canonical form. The API exposes ids as
// `format: uuid` strings, so this is the one place that shape is produced.
func NewUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("ids: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst), nil
}

// NewSecret returns n bytes of randomness, base64url-encoded without padding. Used for
// refresh tokens, which are opaque to the client and only ever compared by hash.
func NewSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("ids: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
