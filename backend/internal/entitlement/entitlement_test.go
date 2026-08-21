package entitlement

import (
	"context"
	"errors"
	"testing"
)

// The most important test in this package. CLAUDE.md §2 rule 6 gives everyone one free pod
// forever, so lane 0 must be usable with no entitlement rows at all — including when the
// map is nil, which is what a failed or empty query produces.
func TestTheFreeLaneNeedsNothing(t *testing.T) {
	for _, opened := range []map[int]bool{nil, {}, {1: true}, {1: true, 2: true}} {
		if err := Validate(FreeSlot, opened); err != nil {
			t.Errorf("the free lane was refused with opened=%v: %v", opened, err)
		}
	}
}

// And it cannot be taken away by anything the map says, including a row that explicitly
// says otherwise. There is no value of `opened` that closes lane 0.
func TestTheFreeLaneCannotBeClosed(t *testing.T) {
	if err := Validate(FreeSlot, map[int]bool{0: false}); err != nil {
		t.Errorf("an entitlement row closed the free lane: %v", err)
	}
}

func TestAnUnopenedLaneIsRefused(t *testing.T) {
	err := Validate(1, map[int]bool{})
	if !errors.Is(err, ErrNotEntitled) {
		t.Errorf("got %v, want ErrNotEntitled", err)
	}
}

func TestAnOpenedLaneIsAllowed(t *testing.T) {
	if err := Validate(2, map[int]bool{2: true}); err != nil {
		t.Errorf("an opened lane was refused: %v", err)
	}
}

func TestALaneOutsideTheRangeIsRefusedEvenIfOpened(t *testing.T) {
	// A row for a slot the product does not have must not create one.
	err := Validate(MaxSlots, map[int]bool{MaxSlots: true})
	if !errors.Is(err, ErrOutOfRange) {
		t.Errorf("got %v, want ErrOutOfRange", err)
	}
	if err := Validate(-1, nil); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("got %v, want ErrOutOfRange", err)
	}
}

func TestLanesAreHandedOutInOrder(t *testing.T) {
	next, err := NextFree(map[int]bool{})
	if err != nil || next != 1 {
		t.Errorf("first additional lane: got %d, %v — want 1", next, err)
	}
	next, err = NextFree(map[int]bool{1: true, 2: true})
	if err != nil || next != 3 {
		t.Errorf("after two: got %d, %v — want 3", next, err)
	}
}

func TestTheCeilingIsReachable(t *testing.T) {
	opened := map[int]bool{}
	for slot := 1; slot < MaxSlots; slot++ {
		opened[slot] = true
	}
	if _, err := NextFree(opened); !errors.Is(err, ErrAtCeiling) {
		t.Errorf("got %v, want ErrAtCeiling", err)
	}
}

// The development verifier must not be a stub that accepts everything: a test that forgets
// to construct a receipt should fail rather than silently pass.
func TestTheDevelopmentVerifierRejectsWhatIsNotADevelopmentReceipt(t *testing.T) {
	ctx := context.Background()
	for _, receipt := range []string{"", "not-a-receipt", "dev:", "DEV:x"} {
		if _, err := (DevVerifier{}).Verify(ctx, SourceAppStore, receipt); !errors.Is(err, ErrReceiptRejected) {
			t.Errorf("receipt %q: got %v, want ErrReceiptRejected", receipt, err)
		}
	}
	if _, err := (DevVerifier{}).Verify(ctx, Source("stripe"), "dev:x"); !errors.Is(err, ErrReceiptRejected) {
		t.Errorf("an unknown source verified: %v", err)
	}
}

// Replaying a receipt must produce the same transaction id, which is what makes claiming
// idempotent rather than a way to open a lane per retry.
func TestTheSameReceiptAlwaysProducesTheSameTransactionID(t *testing.T) {
	ctx := context.Background()
	first, err := DevVerifier{}.Verify(ctx, SourceAppStore, "dev:order-1")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	second, err := DevVerifier{}.Verify(ctx, SourceAppStore, "dev:order-1")
	if err != nil {
		t.Fatalf("Verify again: %v", err)
	}
	if first.ExternalID != second.ExternalID {
		t.Errorf("the same receipt produced %q then %q", first.ExternalID, second.ExternalID)
	}
	other, err := DevVerifier{}.Verify(ctx, SourceAppStore, "dev:order-2")
	if err != nil {
		t.Fatalf("Verify other: %v", err)
	}
	if other.ExternalID == first.ExternalID {
		t.Error("two different receipts produced the same transaction id")
	}
	// And the receipt itself does not survive into what is stored.
	if first.ExternalID == "order-1" || len(first.ExternalID) != 32 {
		t.Errorf("the transaction id looks like the receipt: %q", first.ExternalID)
	}
}

// Different stores must not collide on a transaction id.
func TestTheSameReceiptFromDifferentStoresDiffers(t *testing.T) {
	ctx := context.Background()
	apple, _ := DevVerifier{}.Verify(ctx, SourceAppStore, "dev:order-1")
	play, _ := DevVerifier{}.Verify(ctx, SourcePlayStore, "dev:order-1")
	if apple.ExternalID == play.ExternalID {
		t.Error("the same receipt body collided across stores")
	}
}
