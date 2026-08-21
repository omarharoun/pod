// Package push delivers notifications to phones.
//
// This is the one place the server produces a user-facing sentence rather than a copy key
// (ADR-003) — a push payload has nowhere to put a key, because there is no client running
// to resolve it. Everything sent from here is resolved from content/microcopy.json at send
// time, so the catalogue stays the single source of wording and copylint still covers it.
package push

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/omarharoun/pod/backend/internal/copy"
)

// Platform is which provider a token belongs to.
type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
)

// Message is one notification, already resolved.
type Message struct {
	Token    string
	Platform Platform
	Title    string
	Body     string
	// Thread groups notifications on the device so a pod's week does not arrive as five
	// separate rows in someone's notification centre.
	Thread string
}

// ErrTokenRetired means the provider says this token is dead and should be dropped.
var ErrTokenRetired = fmt.Errorf("push: token retired by the provider")

// Sender delivers a message. APNs and FCM in production; a log in development.
type Sender interface {
	Send(ctx context.Context, message Message) error
}

// Logger writes notifications to the log instead of sending them, so the whole pipeline
// runs locally. Config refuses to select it outside development, as with the mailer
// (ADR-002) and the blob store (ADR-015).
type Logger struct{ Log *slog.Logger }

func (l Logger) Send(ctx context.Context, message Message) error {
	// The token is a credential for reaching somebody's phone. It is never logged, here
	// or anywhere else.
	l.Log.InfoContext(ctx, "push (development sender — not actually sent)",
		slog.String("platform", string(message.Platform)),
		slog.String("title", message.Title),
		slog.String("body", message.Body),
		slog.String("thread", message.Thread),
	)
	return nil
}

// Resolve turns a copy key and its parameters into a title and body.
//
// A notification's title key and body key are conventionally `<base>.title` and
// `<base>.body`; the stored key is the base.
func Resolve(base copy.Key, params map[string]string) (title, body string, err error) {
	title, err = copy.Resolve(base+".title", params)
	if err != nil {
		return "", "", err
	}
	body, err = copy.Resolve(base+".body", params)
	if err != nil {
		return "", "", err
	}
	return title, body, nil
}
