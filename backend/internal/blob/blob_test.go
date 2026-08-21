package blob

import (
	"context"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"
)

const secret = "a-development-signing-secret-value"

func newLocal(t *testing.T) *Local {
	t.Helper()
	store, err := NewLocal(t.TempDir(), "http://localhost:8080", secret)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return store
}

func queryOf(t *testing.T, rawURL string) (string, url.Values) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	return strings.TrimPrefix(parsed.Path, "/blobs/"), parsed.Query()
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	now := time.Now()
	sig := Signature{Key: "proof/w/p", Operation: OperationGet, ExpiresAt: now.Add(time.Minute)}
	values, err := url.ParseQuery(Sign(secret, sig))
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	if !Verify(secret, "proof/w/p", values, now) {
		t.Fatal("a freshly signed link did not verify")
	}
}

func TestSignatureExpires(t *testing.T) {
	now := time.Now()
	values, err := url.ParseQuery(Sign(secret, Signature{
		Key: "proof/w/p", Operation: OperationGet, ExpiresAt: now.Add(time.Minute),
	}))
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	if Verify(secret, "proof/w/p", values, now.Add(2*time.Minute)) {
		t.Fatal("an expired link still verified — the leak window would be unbounded")
	}
}

// A link for one proof must not open another, and a read link must not become a write.
func TestSignatureIsBoundToItsKeyAndOperation(t *testing.T) {
	now := time.Now()
	values, err := url.ParseQuery(Sign(secret, Signature{
		Key: "proof/w/mine", Operation: OperationGet, ExpiresAt: now.Add(time.Minute),
	}))
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	if Verify(secret, "proof/w/theirs", values, now) {
		t.Fatal("a link for one object opened another")
	}

	retargeted := url.Values{}
	for k, v := range values {
		retargeted[k] = v
	}
	retargeted.Set("op", OperationPut)
	if Verify(secret, "proof/w/mine", retargeted, now) {
		t.Fatal("a read link was upgraded to a write")
	}
}

func TestSignatureIsBoundToTheSecret(t *testing.T) {
	now := time.Now()
	values, err := url.ParseQuery(Sign("a-completely-different-secret-x", Signature{
		Key: "proof/w/p", Operation: OperationGet, ExpiresAt: now.Add(time.Minute),
	}))
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	if Verify(secret, "proof/w/p", values, now) {
		t.Fatal("a link signed with another secret verified")
	}
}

func TestVerifyRejectsGarbage(t *testing.T) {
	now := time.Now()
	for name, values := range map[string]url.Values{
		"empty":        {},
		"no signature": {"op": {"get"}, "expires": {"99999999999"}},
		"bad expiry":   {"op": {"get"}, "expires": {"soon"}, "sig": {"x"}},
		"unknown op":   {"op": {"delete"}, "expires": {"99999999999"}, "sig": {"x"}},
	} {
		t.Run(name, func(t *testing.T) {
			if Verify(secret, "proof/w/p", values, now) {
				t.Fatal("verified")
			}
		})
	}
}

func TestSafeKey(t *testing.T) {
	for _, good := range []string{"proof/week/proof", "proof/a/b"} {
		if !SafeKey(good) {
			t.Errorf("SafeKey(%q) = false", good)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "proof/../../etc/passwd", "other/thing", "../proof/x"} {
		if SafeKey(bad) {
			t.Errorf("SafeKey(%q) = true", bad)
		}
	}
}

func TestKeyCarriesNoUserID(t *testing.T) {
	key := Key("week-1", "proof-1")
	if strings.Contains(key, "user") {
		t.Fatalf("key %q looks like it carries a user id — keys end up in URLs and HTTP logs", key)
	}
	if !strings.HasPrefix(key, WeekPrefix("week-1")) {
		t.Fatalf("key %q is not under its week's prefix, so a week cannot be swept as one", key)
	}
}

func TestLocalStoreRoundTrip(t *testing.T) {
	store := newLocal(t)
	ctx := context.Background()
	key := Key("week-1", "proof-1")

	exists, err := store.Exists(ctx, key)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("a fresh store already has the object")
	}

	upload, err := store.SignUpload(ctx, key, "image/heic", 1234, time.Minute)
	if err != nil {
		t.Fatalf("SignUpload: %v", err)
	}
	if upload.Method != "PUT" || upload.Headers["Content-Type"] != "image/heic" {
		t.Fatalf("upload slot: %+v", upload)
	}
	signedKey, values := queryOf(t, upload.URL)
	if signedKey != key {
		t.Fatalf("signed key %q, want %q", signedKey, key)
	}
	if !store.VerifySignature(signedKey, values, time.Now()) {
		t.Fatal("the store did not accept its own upload signature")
	}

	if err := store.Put(key, strings.NewReader("ten seconds of proof")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	exists, err = store.Exists(ctx, key)
	if err != nil || !exists {
		t.Fatalf("Exists after Put: %v %v", exists, err)
	}

	reader, err := store.Open(key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	_ = reader.Close()
	if string(body) != "ten seconds of proof" {
		t.Fatalf("got %q", body)
	}

	// Ephemerality is a promise: deleting must actually remove the bytes, and deleting
	// twice must not be an error (CLAUDE.md §2 rule 4).
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Open(key); err != ErrNotFound {
		t.Fatalf("the bytes survived deletion: %v", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("deleting what is already gone should be fine, got %v", err)
	}
}

func TestLocalStoreRefusesToEscapeItsRoot(t *testing.T) {
	store := newLocal(t)
	ctx := context.Background()
	for _, key := range []string{"../escape", "proof/../../escape", "/absolute"} {
		if _, err := store.SignUpload(ctx, key, "image/heic", 1, time.Minute); err == nil {
			t.Errorf("SignUpload(%q) succeeded", key)
		}
		if err := store.Put(key, strings.NewReader("x")); err == nil {
			t.Errorf("Put(%q) succeeded", key)
		}
	}
}

func TestEmptyUploadDoesNotCountAsUploaded(t *testing.T) {
	store := newLocal(t)
	key := Key("week-1", "proof-1")
	if err := store.Put(key, strings.NewReader("")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	exists, err := store.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("a zero-byte upload counted as a proof — the pod would see an empty tile")
	}
}
