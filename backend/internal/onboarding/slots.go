package onboarding

import (
	"context"
	"errors"
	"fmt"

	"github.com/omarharoun/pod/backend/internal/entitlement"
	"github.com/omarharoun/pod/backend/internal/store"
)

// Claim opens the next lane against a verified purchase.
//
// Idempotent on the store's transaction id, so a client that retries — which every store
// SDK does — gets the lane it already opened rather than another one. The receipt is never
// logged and nothing is kept from it beyond the source and that id (CLAUDE.md §8).
func (s *Service) Claim(ctx context.Context, userID, source, receipt string) (Lane, error) {
	if s.verifier == nil {
		return Lane{}, errors.New("onboarding: no receipt verifier configured")
	}
	purchase, err := s.verifier.Verify(ctx, entitlement.Source(source), receipt)
	if err != nil {
		// Deliberately not wrapped with the receipt, or any part of it, in the message.
		return Lane{}, fmt.Errorf("%w", entitlement.ErrReceiptRejected)
	}

	// A replay returns the lane it already opened. A replay against a *different* account
	// is not a retry — it is one receipt trying to open two lanes — and is refused.
	owner, slot, err := s.store.SlotForExternalID(ctx, string(purchase.Source), purchase.ExternalID)
	switch {
	case err == nil && owner == userID:
		return s.laneFor(ctx, userID, slot)
	case err == nil:
		return Lane{}, entitlement.ErrReceiptRejected
	case !errors.Is(err, store.ErrNotFound):
		return Lane{}, fmt.Errorf("onboarding: check transaction: %w", err)
	}

	opened, err := s.store.ListEntitlements(ctx, userID)
	if err != nil {
		return Lane{}, fmt.Errorf("onboarding: read entitlements: %w", err)
	}
	next, err := entitlement.NextFree(opened)
	if err != nil {
		return Lane{}, err
	}
	if err := s.store.GrantSlot(ctx, userID, next, string(purchase.Source), purchase.ExternalID, s.clock.Now()); err != nil {
		if errors.Is(err, store.ErrConflict) {
			// Two claims raced. Whichever won, the caller has a lane; read it back
			// rather than reporting a failure for something that succeeded.
			return s.laneFor(ctx, userID, next)
		}
		return Lane{}, fmt.Errorf("onboarding: grant slot: %w", err)
	}
	return s.laneFor(ctx, userID, next)
}

func (s *Service) laneFor(ctx context.Context, userID string, slot int) (Lane, error) {
	lanes, err := s.Lanes(ctx, userID)
	if err != nil {
		return Lane{}, err
	}
	for _, lane := range lanes {
		if lane.Slot == slot {
			return lane, nil
		}
	}
	return Lane{}, store.ErrNotFound
}
