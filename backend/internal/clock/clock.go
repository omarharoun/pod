// Package clock exists so that anything time-dependent — code expiry, token lifetime,
// cycle boundaries — can be tested without sleeping.
package clock

import "time"

// Clock reports the current time. All timestamps in this service are UTC (CLAUDE.md §5);
// pod cycles anchor to the pod's shared timezone, which is a display and boundary
// concern layered on top of UTC, never a substitute for it.
type Clock interface {
	Now() time.Time
}

// System reads the wall clock.
type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

// Fixed is a test clock. Advance moves it deliberately.
type Fixed struct{ current time.Time }

func NewFixed(t time.Time) *Fixed { return &Fixed{current: t.UTC()} }

func (f *Fixed) Now() time.Time { return f.current }

func (f *Fixed) Advance(d time.Duration) { f.current = f.current.Add(d) }
