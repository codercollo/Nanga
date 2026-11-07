package domain

import (
	"fmt"
	"time"

	"github.com/codercollo/nanga/internal/platform/money"
)

// HoldStatus is the hold lifecycle: active -> released | captured.
type HoldStatus string

const (
	HoldActive   HoldStatus = "active"
	HoldReleased HoldStatus = "released"
	HoldCaptured HoldStatus = "captured"
)

// Hold reserves funds on an account (e.g. while a withdrawal is in flight).
// Reference is the caller's unique key (typically the payment id).
type Hold struct {
	ID         string
	AccountID  string
	Reference  string
	Amount     money.Money
	Status     HoldStatus
	JournalID  string // set when captured
	CreatedAt  time.Time
	ResolvedAt time.Time // zero while active
}

// NewHold validates and builds an active hold.
func NewHold(id, accountID, reference string, amount money.Money, now time.Time) (Hold, error) {
	if id == "" || accountID == "" {
		return Hold{}, invalid("hold id and account required")
	}
	if reference == "" || len(reference) > MaxKeyLen {
		return Hold{}, invalid("hold reference required (<=%d chars)", MaxKeyLen)
	}
	if !amount.IsPositive() || !amount.Currency.Valid() {
		return Hold{}, invalid("hold amount must be positive in a supported currency")
	}
	return Hold{ID: id, AccountID: accountID, Reference: reference, Amount: amount, Status: HoldActive, CreatedAt: now}, nil
}

// Release marks an active hold released.
func (h Hold) Release(now time.Time) (Hold, error) {
	if h.Status != HoldActive {
		return Hold{}, fmt.Errorf("%w: %s is %s", ErrHoldNotActive, h.Reference, h.Status)
	}
	h.Status, h.ResolvedAt = HoldReleased, now
	return h, nil
}

// Capture marks an active hold captured by a posted journal.
func (h Hold) Capture(now time.Time, journalID string) (Hold, error) {
	if h.Status != HoldActive {
		return Hold{}, fmt.Errorf("%w: %s is %s", ErrHoldNotActive, h.Reference, h.Status)
	}
	if journalID == "" {
		return Hold{}, invalid("capture requires journal id")
	}
	h.Status, h.ResolvedAt, h.JournalID = HoldCaptured, now, journalID
	return h, nil
}
