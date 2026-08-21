package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Local keeps proof bytes on disk and signs URLs pointing at the service's own blob
// routes.
//
// Development only, and `config` refuses to select it outside development — the same
// stance as the logging mailer (ADR-002). In production the URLs point at object storage
// and the bytes never touch the service at all (ADR-008). The interface is identical so
// the week loop cannot tell the difference.
type Local struct {
	root    string
	baseURL string
	secret  string
}

func NewLocal(root, baseURL, secret string) (*Local, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("blob: create root: %w", err)
	}
	return &Local{root: root, baseURL: strings.TrimRight(baseURL, "/"), secret: secret}, nil
}

func (l *Local) SignUpload(_ context.Context, key, contentType string, byteSize int64, ttl time.Duration) (Upload, error) {
	if !SafeKey(key) {
		return Upload{}, fmt.Errorf("blob: refusing to sign %q", key)
	}
	expiresAt := time.Now().Add(ttl)
	return Upload{
		Key:    key,
		URL:    l.url(key, Signature{Key: key, Operation: OperationPut, ExpiresAt: expiresAt}),
		Method: "PUT",
		Headers: map[string]string{
			"Content-Type":   contentType,
			"Content-Length": fmt.Sprintf("%d", byteSize),
		},
		ExpiresAt: expiresAt,
	}, nil
}

func (l *Local) SignView(_ context.Context, key string, ttl time.Duration) (View, error) {
	if !SafeKey(key) {
		return View{}, fmt.Errorf("blob: refusing to sign %q", key)
	}
	expiresAt := time.Now().Add(ttl)
	return View{
		URL:       l.url(key, Signature{Key: key, Operation: OperationGet, ExpiresAt: expiresAt}),
		ExpiresAt: expiresAt,
	}, nil
}

func (l *Local) Exists(_ context.Context, key string) (bool, error) {
	path, err := l.path(key)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("blob: stat: %w", err)
	}
	return info.Size() > 0, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	path, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("blob: delete: %w", err)
	}
	return nil
}

// Put stores bytes. Called by the service's blob route after it has verified a signature,
// never by the week loop.
func (l *Local) Put(key string, body io.Reader) error {
	path, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("blob: create directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("blob: open for write: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := io.Copy(file, body); err != nil {
		return fmt.Errorf("blob: write: %w", err)
	}
	return nil
}

// Open returns the bytes behind a key.
func (l *Local) Open(key string) (io.ReadCloser, error) {
	path, err := l.path(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("blob: open: %w", err)
	}
	return file, nil
}

// VerifySignature checks a request's query against the store's secret.
func (l *Local) VerifySignature(key string, values url.Values, now time.Time) bool {
	return Verify(l.secret, key, values, now)
}

func (l *Local) url(key string, sig Signature) string {
	return fmt.Sprintf("%s/blobs/%s?%s", l.baseURL, key, Sign(l.secret, sig))
}

func (l *Local) path(key string) (string, error) {
	if !SafeKey(key) {
		return "", fmt.Errorf("blob: refusing to touch %q", key)
	}
	path := filepath.Join(l.root, filepath.FromSlash(key))
	// Belt and braces over SafeKey: whatever the key said, the result must be under root.
	root, err := filepath.Abs(l.root)
	if err != nil {
		return "", fmt.Errorf("blob: resolve root: %w", err)
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("blob: resolve path: %w", err)
	}
	if !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("blob: %q escapes the storage root", key)
	}
	return resolved, nil
}
