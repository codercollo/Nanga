// Package app implements ledger use cases against ports declared here. It
// never imports adapters, generated code, or drivers.
package app

import (
	"context"
	"time"

	"github.com/codercollo/nanga/internal/ledger/domain"
	"github.com/codercollo/nanga/internal/platform/money"
)

// Clock supplies the current time (injectable for tests).
type Clock interface{ Now() time.Time }

// SystemClock is the real clock.
type SystemClock struct{}

// Now returns the current UTC time.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// IDGenerator supplies unique identifiers.
type IDGenerator interface{ NewID() string }

// Reader is the read side of the repository. Missing rows are reported with
// the matching domain.Err*NotFound error.
type Reader interface {
	GetAccount(ctx context.Context, id string) (domain.Account, error)
	GetAccountByOwner(ctx context.Context, ownerRef string, cur money.Currency) (domain.Account, error)
	GetAccountByCode(ctx context.Context, code string) (domain.Account, error)
	// ListAccounts pages by ascending id: accounts with id > afterID.
	ListAccounts(ctx context.Context, afterID string, limit int) ([]domain.Account, error)
	GetJournalByKey(ctx context.Context, idempotencyKey string) (domain.Journal, error)
	// GetReversalOf returns the journal that reverses journalID, or
	// ErrJournalNotFound when it has not been reversed.
	GetReversalOf(ctx context.Context, journalID string) (domain.Journal, error)
	GetHoldByRef(ctx context.Context, reference string) (domain.Hold, error)
	// ListStatement returns up to limit entries of the account, newest first,
	// with Seq < beforeSeq (beforeSeq <= 0 means "from the newest").
	ListStatement(ctx context.Context, accountID string, beforeSeq int64, limit int) ([]domain.StatementLine, error)
	// EntryTotals sums every posted entry for the account by side.
	EntryTotals(ctx context.Context, accountID string) (debits, credits int64, err error)
	// TrialBalance totals every posted entry per currency.
	TrialBalance(ctx context.Context) ([]domain.TrialBalanceLine, error)
}

// Tx is a unit of work. Reads inside a Tx see committed state only.
//
// Locking contract: LockAccounts takes exclusive row locks held until the
// transaction ends. Callers MUST pass ids in ascending order (use
// domain.SortedUniqueIDs); implementations reject unordered locking.
// UpdateAccount must only be called for accounts locked by this Tx.
type Tx interface {
	Reader
	CreateAccount(ctx context.Context, a domain.Account) error // ErrAccountExists
	LockAccounts(ctx context.Context, ids []string) ([]domain.Account, error)
	// UpdateAccount persists balance/held/status/version, guarded by
	// expectedVersion (domain.ErrConcurrentModification on mismatch).
	UpdateAccount(ctx context.Context, a domain.Account, expectedVersion int64) error
	InsertJournal(ctx context.Context, j domain.Journal) error // ErrDuplicateKey
	InsertHold(ctx context.Context, h domain.Hold) error       // ErrDuplicateKey
	UpdateHold(ctx context.Context, h domain.Hold) error
}

// Repository opens transactions and serves non-transactional reads.
// If fn returns an error the transaction is rolled back.
type Repository interface {
	Reader
	WithTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}
