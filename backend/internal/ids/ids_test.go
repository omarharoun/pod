package ids

import (
	"regexp"
	"testing"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewUUIDShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id, err := NewUUID()
		if err != nil {
			t.Fatalf("NewUUID: %v", err)
		}
		if !uuidV4.MatchString(id) {
			t.Fatalf("not a v4 uuid: %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate uuid after %d draws: %q", i, id)
		}
		seen[id] = true
	}
}

func TestNewSecretIsUnpaddedURLSafe(t *testing.T) {
	s, err := NewSecret(32)
	if err != nil {
		t.Fatalf("NewSecret: %v", err)
	}
	if len(s) != 43 {
		t.Fatalf("expected 43 chars for 32 bytes, got %d (%q)", len(s), s)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			t.Fatalf("secret contains %q, which is not URL-safe", r)
		}
	}
}
