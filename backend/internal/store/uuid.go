package store

import "github.com/omarharoun/pod/backend/internal/ids"

// newUUID is indirected so the store's identifier shape stays in one place.
func newUUID() (string, error) { return ids.NewUUID() }
