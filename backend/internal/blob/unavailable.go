package blob

import (
	"context"
	"errors"
	"time"
)

// Unavailable is a Store for processes that never touch proof bytes.
//
// cmd/matcher needs a week service in order to open a new pod's first week, and a week
// service takes a blob store — but the matcher never signs an upload, never issues a view
// link and never sweeps. Handing it a real store would give a process a capability it has
// no business having; handing it nil would panic the first time anything changed.
//
// Every method fails loudly. If one of them is ever called, that is a wiring mistake and
// it should look like one rather than silently returning an empty result.
type Unavailable struct{}

// ErrUnavailable is returned by every method on Unavailable.
var ErrUnavailable = errors.New("blob: this process has no blob store")

func (Unavailable) SignUpload(context.Context, string, string, int64, time.Duration) (Upload, error) {
	return Upload{}, ErrUnavailable
}

func (Unavailable) SignView(context.Context, string, time.Duration) (View, error) {
	return View{}, ErrUnavailable
}

func (Unavailable) Exists(context.Context, string) (bool, error) {
	return false, ErrUnavailable
}

func (Unavailable) Delete(context.Context, string) error {
	return ErrUnavailable
}
