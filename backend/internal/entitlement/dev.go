package entitlement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// DevVerifier accepts a receipt of the form "dev:<anything>" and derives a stable
// transaction id from it.
//
// It exists so the whole M7 path — claim, entitle, second intent, second pod — can be
// exercised end to end without App Store credentials. `config` refuses to select it
// outside development, exactly as it refuses the dev mailer and the local blob store.
//
// It is deliberately not a stub that accepts everything: a receipt without the prefix is
// rejected, so a test that forgets to construct one fails rather than silently passing.
type DevVerifier struct{}

const devPrefix = "dev:"

func (DevVerifier) Verify(_ context.Context, source Source, receipt string) (Purchase, error) {
	if source != SourceAppStore && source != SourcePlayStore {
		return Purchase{}, fmt.Errorf("%w: unknown source %q", ErrReceiptRejected, source)
	}
	if !strings.HasPrefix(receipt, devPrefix) {
		return Purchase{}, fmt.Errorf("%w: not a development receipt", ErrReceiptRejected)
	}
	body := strings.TrimPrefix(receipt, devPrefix)
	if body == "" {
		return Purchase{}, fmt.Errorf("%w: empty development receipt", ErrReceiptRejected)
	}
	// Hashed rather than stored: the id is stable across replays of the same receipt,
	// which is what makes claiming idempotent, and it keeps whatever the caller put in
	// the receipt out of the database.
	sum := sha256.Sum256([]byte(string(source) + ":" + body))
	return Purchase{Source: source, ExternalID: hex.EncodeToString(sum[:16])}, nil
}
