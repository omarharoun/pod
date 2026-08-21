// Package mail sends the one transactional email M1 needs.
package mail

import (
	"context"
	"log/slog"

	"github.com/omarharoun/pod/backend/internal/copy"
)

// Mailer delivers the login code. Body text is resolved from the copy catalogue, never
// written here — see ADR-003.
type Mailer interface {
	SendLoginCode(ctx context.Context, email, code string, ttlMinutes int) error
}

// Logger is the development mailer: it writes the code to the log so a local sign-in
// works without an email provider. Config refuses to select it outside development.
type Logger struct{ Log *slog.Logger }

func (l Logger) SendLoginCode(ctx context.Context, email, code string, ttlMinutes int) error {
	params := map[string]string{"code": code, "minutes": itoa(ttlMinutes)}
	subject, err := copy.Resolve(copy.KeyEmailLoginCodeSubject, params)
	if err != nil {
		return err
	}
	body, err := copy.Resolve(copy.KeyEmailLoginCodeBody, params)
	if err != nil {
		return err
	}
	l.Log.InfoContext(ctx, "login code (development mailer — not actually sent)",
		slog.String("email", email),
		slog.String("subject", subject),
		slog.String("body", body),
	)
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
