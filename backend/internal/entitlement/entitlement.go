// Package entitlement answers one question: may this person use this lane?
//
// The answer for lane 0 is yes, unconditionally, and that is the most important line in
// this package. CLAUDE.md §2 rule 6 gives everyone one free pod forever, and the free loop
// must not depend on the presence of a payment record — a missing, expired or
// badly-migrated row would otherwise take the core product away from someone who never
// paid for it and never needed to.
package entitlement

import (
	"context"
	"errors"
	"fmt"
)

// FreeSlot is the lane everybody has. It needs no entitlement row and never will.
const FreeSlot = 0

// MaxSlots is a structural ceiling above the paywall, unrelated to what anyone paid:
// someone running five small things a week is not doing the product in CLAUDE.md §1.
// A judgement, recorded in ADR-026.
const MaxSlots = 5

var (
	// ErrNotEntitled is returned for a lane the caller has not opened.
	ErrNotEntitled = errors.New("entitlement: that lane is not open")
	// ErrOutOfRange is a slot number outside 0..MaxSlots-1.
	ErrOutOfRange = errors.New("entitlement: no such lane")
	// ErrReceiptRejected is a receipt the store did not verify.
	ErrReceiptRejected = errors.New("entitlement: the receipt did not verify")
	// ErrAtCeiling is returned when every lane is already open.
	ErrAtCeiling = errors.New("entitlement: every lane is already open")
)

// Source is which store a receipt came from.
type Source string

const (
	SourceAppStore  Source = "app_store"
	SourcePlayStore Source = "play_store"
)

// Purchase is what survives verification.
//
// The transaction id and the source, and nothing else. Not the price, not the currency,
// not the country, not the account it was bought with. The server needs to know *that* a
// lane is paid for; everything beyond that is data the feature does not need and so does
// not hold (CLAUDE.md §8).
type Purchase struct {
	Source     Source
	ExternalID string
}

// Verifier checks a receipt with the store that issued it.
//
// A seam, for the same reason the mailer (ADR-002), the blob store (ADR-015) and the push
// sender are: verifying an App Store receipt needs credentials this repository does not
// and should not contain. `config` refuses to select the development implementation
// outside development.
type Verifier interface {
	Verify(ctx context.Context, source Source, receipt string) (Purchase, error)
}

// InRange reports whether a slot number is one the product has.
func InRange(slot int) bool { return slot >= 0 && slot < MaxSlots }

// IsFree reports whether a slot needs no entitlement.
//
// Written as its own function, and called rather than inlined, so that every place the
// question is asked gives the same answer. A `slot == 0` scattered through handlers is a
// rule that can be got wrong in one place.
func IsFree(slot int) bool { return slot == FreeSlot }

// Validate is the check every slot-aware entry point makes.
//
// It takes the set of lanes the caller has opened, rather than reading them, so that the
// free-lane answer cannot depend on a query succeeding.
func Validate(slot int, opened map[int]bool) error {
	if !InRange(slot) {
		return fmt.Errorf("%w: %d", ErrOutOfRange, slot)
	}
	if IsFree(slot) {
		return nil
	}
	if !opened[slot] {
		return fmt.Errorf("%w: %d", ErrNotEntitled, slot)
	}
	return nil
}

// NextFree is the lowest lane the caller has not opened, or ErrAtCeiling.
//
// Lanes are handed out in order rather than chosen, because a lane is not a thing anyone
// has an opinion about — the goal in it is.
func NextFree(opened map[int]bool) (int, error) {
	for slot := FreeSlot + 1; slot < MaxSlots; slot++ {
		if !opened[slot] {
			return slot, nil
		}
	}
	return 0, ErrAtCeiling
}
