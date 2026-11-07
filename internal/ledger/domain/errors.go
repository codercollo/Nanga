// Package domain holds the ledger's pure business rules: accounts, journals,
// holds and their invariants. It imports only the standard library and
// platform/money.
package domain

import (
	"errors"
	"fmt"
)

// Sentinel errors. Adapters and callers match with errors.Is.
var (
	// ErrInvalid is the root of all argument/shape validation failures.
	ErrInvalid = errors.New("ledger: invalid argument")
	// ErrUnbalancedJournal: total debits differ from total credits.
	ErrUnbalancedJournal = fmt.Errorf("%w: journal debits and credits differ", ErrInvalid)
	// ErrCurrencyMismatch: entry/account/hold currencies disagree.
	ErrCurrencyMismatch = fmt.Errorf("%w: currency mismatch", ErrInvalid)

	ErrInsufficientFunds   = errors.New("ledger: insufficient funds")
	ErrAccountNotActive    = errors.New("ledger: account not active")
	ErrAccountNotFound     = errors.New("ledger: account not found")
	ErrAccountExists       = errors.New("ledger: account already exists")
	ErrJournalNotFound     = errors.New("ledger: journal not found")
	ErrHoldNotFound        = errors.New("ledger: hold not found")
	ErrHoldNotActive       = errors.New("ledger: hold is not active")
	ErrHoldConflict        = errors.New("ledger: hold reference reused with different parameters")
	ErrHoldMismatch        = errors.New("ledger: journal does not match held amount")
	ErrIdempotencyConflict = errors.New("ledger: idempotency key reused with different content")
	ErrBalanceMismatch     = errors.New("ledger: stored balance differs from entries")
	ErrAlreadyReversed     = errors.New("ledger: journal already reversed")

	// ErrDuplicateKey is returned by repositories when a unique key (journal
	// idempotency key, hold reference) already exists. The service treats it
	// as "lost a race" and re-reads.
	ErrDuplicateKey = errors.New("ledger: duplicate key")
	// ErrConcurrentModification: optimistic version guard failed. Indicates a
	// locking bug, never normal contention.
	ErrConcurrentModification = errors.New("ledger: concurrent modification")
	// ErrOverflow: int64 arithmetic would overflow.
	ErrOverflow = errors.New("ledger: arithmetic overflow")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}
