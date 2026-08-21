// Package blob is where proof bytes live.
//
// ADR-008: the bytes never pass through the API. A client is handed a short-lived,
// single-use URL, PUTs straight to storage, and then tells the API it is done. Reads work
// the same way. This package is the seam that makes that true of production and testable
// in development.
package blob

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound is returned when a key has no bytes behind it — including after the sweep
// has done its job.
var ErrNotFound = errors.New("blob: not found")

// Upload is a one-time slot to PUT bytes into.
type Upload struct {
	Key       string
	URL       string
	Method    string
	Headers   map[string]string
	ExpiresAt time.Time
}

// View is a short-lived link to read one object.
type View struct {
	URL       string
	ExpiresAt time.Time
}

// Store is where proof bytes go. The production implementation is object storage; the
// development one is the local disk. Neither ever hands bytes to the API tier.
type Store interface {
	// SignUpload issues a slot for exactly this content type and size.
	SignUpload(ctx context.Context, key, contentType string, byteSize int64, ttl time.Duration) (Upload, error)
	// SignView issues a read link. Its lifetime is minutes, and always shorter than the
	// proof's own expiry.
	SignView(ctx context.Context, key string, ttl time.Duration) (View, error)
	// Exists reports whether anything was actually uploaded to a slot, so an
	// uncommitted proof never becomes visible to a pod.
	Exists(ctx context.Context, key string) (bool, error)
	// Delete removes the bytes. Deleting what is already gone is not an error — the
	// promise is that it is not there, not that we were the ones to remove it.
	Delete(ctx context.Context, key string) error
}

// Signature covers a key, an operation and an expiry.
//
// Written by hand rather than borrowed from a cloud SDK because the development store
// has to verify them too, and because the whole scheme is four fields — a stolen link
// must not be extendable, retargetable, or usable after its minute is up.
type Signature struct {
	Key       string
	Operation string
	ExpiresAt time.Time
}

const (
	OperationPut = "put"
	OperationGet = "get"
)

// Sign returns the query string that authorises one operation on one key.
func Sign(secret string, sig Signature) string {
	expires := strconv.FormatInt(sig.ExpiresAt.Unix(), 10)
	values := url.Values{}
	values.Set("op", sig.Operation)
	values.Set("expires", expires)
	values.Set("sig", mac(secret, sig.Key, sig.Operation, expires))
	return values.Encode()
}

// Verify checks a signature and its expiry. It returns a plain bool: a caller has no use
// for the difference between forged and expired, and neither does an attacker.
func Verify(secret, key string, values url.Values, now time.Time) bool {
	expires := values.Get("expires")
	seconds, err := strconv.ParseInt(expires, 10, 64)
	if err != nil {
		return false
	}
	if now.After(time.Unix(seconds, 0)) {
		return false
	}
	operation := values.Get("op")
	if operation != OperationPut && operation != OperationGet {
		return false
	}
	return hmac.Equal([]byte(values.Get("sig")), []byte(mac(secret, key, operation, expires)))
}

func mac(secret, key, operation, expires string) string {
	digest := hmac.New(sha256.New, []byte(secret))
	digest.Write([]byte(key))
	digest.Write([]byte{0})
	digest.Write([]byte(operation))
	digest.Write([]byte{0})
	digest.Write([]byte(expires))
	return base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
}

// Key is where a proof's bytes live.
//
// Namespaced by week so that a week's worth of proof can be swept as a prefix, and
// carrying no user id — an object key is the one part of this that ends up in a URL, in a
// storage bucket listing, and in somebody's HTTP logs.
func Key(weekID, proofID string) string {
	return fmt.Sprintf("proof/%s/%s", weekID, proofID)
}

// WeekPrefix is every proof in one week.
func WeekPrefix(weekID string) string {
	return fmt.Sprintf("proof/%s/", weekID)
}

// SafeKey rejects anything that could escape the storage root. The keys are ours, so this
// is a guard against a bug rather than against a user.
func SafeKey(key string) bool {
	if key == "" || strings.Contains(key, "..") || strings.HasPrefix(key, "/") {
		return false
	}
	return strings.HasPrefix(key, "proof/")
}
