package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/codercollo/nanga/internal/ledger/domain"
	"github.com/codercollo/nanga/internal/platform/money"
)

// EntryInput is one requested journal line.
type EntryInput struct {
	AccountID string
	Side      domain.Side
	Amount    money.Money
}

// PostCmd requests an atomic, balanced posting. IdempotencyKey makes retries
// safe: the same key with the same content replays the original journal.
type PostCmd struct {
	IdempotencyKey string
	Kind           string
	Reference      string
	Description    string
	Entries        []EntryInput
}

// PostResult is the posted journal. Replayed is true when the key had
// already been posted and nothing new was written.
type PostResult struct {
	Journal  domain.Journal
	Replayed bool
}

func (s *Service) buildJournal(cmd PostCmd) (domain.Journal, error) {
	entries := make([]domain.Entry, len(cmd.Entries))
	for i, e := range cmd.Entries {
		entries[i] = domain.Entry{AccountID: e.AccountID, Side: e.Side, Amount: e.Amount}
	}
	return domain.NewJournal(domain.NewJournalParams{
		ID: s.ids.NewID(), IdempotencyKey: cmd.IdempotencyKey, Kind: cmd.Kind,
		Reference: cmd.Reference, Description: cmd.Description,
		Entries: entries, PostedAt: s.clock.Now(),
	})
}

// replayOf returns the stored journal when key was already posted. The second
// result is false when the key is new.
func replayOf(ctx context.Context, r Reader, j domain.Journal) (domain.Journal, bool, error) {
	existing, err := r.GetJournalByKey(ctx, j.IdempotencyKey)
	if errors.Is(err, domain.ErrJournalNotFound) {
		return domain.Journal{}, false, nil
	}
	if err != nil {
		return domain.Journal{}, false, err
	}
	if existing.Fingerprint != j.Fingerprint {
		return domain.Journal{}, false, fmt.Errorf("%w: key %q", domain.ErrIdempotencyConflict, j.IdempotencyKey)
	}
	return existing, true, nil
}

// Post atomically applies a balanced journal. Accounts are locked in
// ascending id order, balances are checked against the non-negative rule, and
// the journal plus balance updates commit together or not at all.
func (s *Service) Post(ctx context.Context, cmd PostCmd) (PostResult, error) {
	j, err := s.buildJournal(cmd)
	if err != nil {
		return PostResult{}, err
	}
	var res PostResult
	err = s.inTx(ctx, func(ctx context.Context, tx Tx) error {
		r, err := postInTx(ctx, tx, j, nil)
		res = r
		return err
	})
	if err != nil {
		return PostResult{}, err
	}
	return res, nil
}

// postInTx is the shared posting path. afterLock, when non-nil, runs once the
// account locks are held and before anything is written (used by Reverse to
// check "already reversed" race-free).
func postInTx(ctx context.Context, tx Tx, j domain.Journal, afterLock func(context.Context) error) (PostResult, error) {
	if existing, ok, err := replayOf(ctx, tx, j); err != nil || ok {
		return PostResult{Journal: existing, Replayed: ok}, err
	}
	locked, err := lockAll(ctx, tx, j.AccountIDs())
	if err != nil {
		return PostResult{}, err
	}
	// Re-check under lock: a concurrent tx with the same key may have
	// committed while we waited.
	if existing, ok, err := replayOf(ctx, tx, j); err != nil || ok {
		return PostResult{Journal: existing, Replayed: ok}, err
	}
	if afterLock != nil {
		if err := afterLock(ctx); err != nil {
			return PostResult{}, err
		}
	}
	posted, err := applyJournal(ctx, tx, j, locked)
	return PostResult{Journal: posted}, err
}

// locked tracks accounts held by the transaction, with the version loaded.
type lockedAccount struct {
	acct    domain.Account // current (possibly modified) state
	version int64          // version when loaded, for the optimistic guard
}

func lockAll(ctx context.Context, tx Tx, ids []string) (map[string]*lockedAccount, error) {
	ids = domain.SortedUniqueIDs(ids)
	accts, err := tx.LockAccounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	m := make(map[string]*lockedAccount, len(accts))
	for _, a := range accts {
		m[a.ID] = &lockedAccount{acct: a, version: a.Version}
	}
	return m, nil
}

// applyJournal applies j to the locked accounts and persists everything.
func applyJournal(ctx context.Context, tx Tx, j domain.Journal, locked map[string]*lockedAccount) (domain.Journal, error) {
	byAccount := map[string][]int{}
	for i, e := range j.Entries {
		byAccount[e.AccountID] = append(byAccount[e.AccountID], i)
	}
	for _, id := range j.AccountIDs() { // ascending, same as lock order
		la, ok := locked[id]
		if !ok {
			return domain.Journal{}, fmt.Errorf("%w: %s", domain.ErrAccountNotFound, id)
		}
		idx := byAccount[id]
		group := make([]domain.Entry, len(idx))
		for k, i := range idx {
			group[k] = j.Entries[i]
		}
		updated, after, err := la.acct.ApplyEntries(group)
		if err != nil {
			return domain.Journal{}, err
		}
		for k, i := range idx {
			j.Entries[i].BalanceAfter = after[k]
		}
		la.acct = updated
	}
	if err := tx.InsertJournal(ctx, j); err != nil {
		return domain.Journal{}, err
	}
	for _, id := range j.AccountIDs() {
		la := locked[id]
		if err := tx.UpdateAccount(ctx, la.acct, la.version); err != nil {
			return domain.Journal{}, err
		}
	}
	return j, nil
}
