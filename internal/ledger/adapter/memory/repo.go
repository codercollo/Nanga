// Package memory is an in-memory ledger repository. It backs unit tests and
// local experiments and deliberately mimics Postgres semantics that the
// concurrency tests depend on: exclusive per-account row locks held until the
// transaction ends, uniqueness enforced at commit, an optimistic version
// guard, and rejection of out-of-order locking (a deadlock risk in Postgres).
package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/codercollo/nanga/internal/ledger/app"
	"github.com/codercollo/nanga/internal/ledger/domain"
	"github.com/codercollo/nanga/internal/platform/money"
)

var _ app.Repository = (*Repo)(nil)

// ErrLockOrder is returned when a transaction locks accounts out of
// ascending-id order. Postgres would only deadlock under load; here it fails
// deterministically so tests catch the bug.
var ErrLockOrder = errors.New("memory: accounts must be locked in ascending id order")

// ErrNotLocked is returned at commit when an account was updated without
// holding its lock.
var ErrNotLocked = errors.New("memory: account updated without lock")

type totals struct{ debits, credits int64 }

// Repo is safe for concurrent use.
type Repo struct {
	mu       sync.RWMutex
	accounts map[string]domain.Account
	locks    map[string]chan struct{} // 1-slot channel = ctx-aware mutex
	byOwner  map[string]string
	byCode   map[string]string
	journals map[string]domain.Journal // by idempotency key
	reversed map[string]string         // original journal id -> reversal key
	holds    map[string]domain.Hold    // by reference
	sums     map[string]totals         // per account entry totals
	stmts    map[string][]domain.StatementLine
	seq      int64
}

// New returns an empty repository.
func New() *Repo {
	return &Repo{
		accounts: map[string]domain.Account{}, locks: map[string]chan struct{}{},
		byOwner: map[string]string{}, byCode: map[string]string{},
		journals: map[string]domain.Journal{}, holds: map[string]domain.Hold{},
		reversed: map[string]string{},
		sums:     map[string]totals{}, stmts: map[string][]domain.StatementLine{},
	}
}

func ownerKey(owner string, cur money.Currency) string { return owner + "|" + string(cur) }

func cloneJournal(j domain.Journal) domain.Journal {
	j.Entries = append([]domain.Entry(nil), j.Entries...)
	return j
}

// ---- Reader ----

func (r *Repo) GetAccount(_ context.Context, id string) (domain.Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.accounts[id]
	if !ok {
		return domain.Account{}, fmt.Errorf("%w: %s", domain.ErrAccountNotFound, id)
	}
	return a, nil
}

func (r *Repo) GetAccountByOwner(ctx context.Context, owner string, cur money.Currency) (domain.Account, error) {
	r.mu.RLock()
	id, ok := r.byOwner[ownerKey(owner, cur)]
	r.mu.RUnlock()
	if !ok {
		return domain.Account{}, fmt.Errorf("%w: owner %s", domain.ErrAccountNotFound, owner)
	}
	return r.GetAccount(ctx, id)
}

func (r *Repo) GetAccountByCode(ctx context.Context, code string) (domain.Account, error) {
	r.mu.RLock()
	id, ok := r.byCode[code]
	r.mu.RUnlock()
	if !ok {
		return domain.Account{}, fmt.Errorf("%w: code %s", domain.ErrAccountNotFound, code)
	}
	return r.GetAccount(ctx, id)
}

func (r *Repo) ListAccounts(_ context.Context, afterID string, limit int) ([]domain.Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.accounts))
	for id := range r.accounts {
		if id > afterID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]domain.Account, len(ids))
	for i, id := range ids {
		out[i] = r.accounts[id]
	}
	return out, nil
}

func (r *Repo) GetJournalByKey(_ context.Context, key string) (domain.Journal, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j, ok := r.journals[key]
	if !ok {
		return domain.Journal{}, fmt.Errorf("%w: key %s", domain.ErrJournalNotFound, key)
	}
	return cloneJournal(j), nil
}

func (r *Repo) GetReversalOf(_ context.Context, journalID string) (domain.Journal, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key, ok := r.reversed[journalID]
	if !ok {
		return domain.Journal{}, fmt.Errorf("%w: no reversal of %s", domain.ErrJournalNotFound, journalID)
	}
	return cloneJournal(r.journals[key]), nil
}

func (r *Repo) ListStatement(_ context.Context, accountID string, beforeSeq int64, limit int) ([]domain.StatementLine, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := r.stmts[accountID]
	out := make([]domain.StatementLine, 0, limit)
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		if beforeSeq > 0 && all[i].Seq >= beforeSeq {
			continue
		}
		out = append(out, all[i])
	}
	return out, nil
}

func (r *Repo) GetHoldByRef(_ context.Context, ref string) (domain.Hold, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.holds[ref]
	if !ok {
		return domain.Hold{}, fmt.Errorf("%w: %s", domain.ErrHoldNotFound, ref)
	}
	return h, nil
}

func (r *Repo) EntryTotals(_ context.Context, accountID string) (int64, int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t := r.sums[accountID]
	return t.debits, t.credits, nil
}

func (r *Repo) TrialBalance(_ context.Context) ([]domain.TrialBalanceLine, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	by := map[money.Currency]*domain.TrialBalanceLine{}
	for _, j := range r.journals {
		l := by[j.Currency]
		if l == nil {
			l = &domain.TrialBalanceLine{Currency: j.Currency}
			by[j.Currency] = l
		}
		d, c := j.Totals()
		l.Debits += d
		l.Credits += c
	}
	out := make([]domain.TrialBalanceLine, 0, len(by))
	for _, l := range by {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Currency < out[k].Currency })
	return out, nil
}

// ---- Transactions ----

// WithTx runs fn; nothing is visible to others until fn returns nil and the
// commit validations pass.
func (r *Repo) WithTx(ctx context.Context, fn func(ctx context.Context, tx app.Tx) error) error {
	tx := &Tx{Repo: r, held: map[string]bool{}}
	defer tx.release()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return r.commit(tx)
}

// Tx is a memory transaction. Writes are staged and applied at commit.
type Tx struct {
	*Repo
	held     map[string]bool
	order    []string
	lastLock string

	newAccounts []domain.Account
	updates     []accountUpdate
	journals    []domain.Journal
	newHolds    []domain.Hold
	holdUpdates []domain.Hold
}

type accountUpdate struct {
	acct    domain.Account
	version int64
}

func (t *Tx) release() {
	for i := len(t.order) - 1; i >= 0; i-- {
		<-t.Repo.lockChan(t.order[i])
	}
}

func (r *Repo) lockChan(id string) chan struct{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.locks[id]
}

func (t *Tx) LockAccounts(ctx context.Context, ids []string) ([]domain.Account, error) {
	out := make([]domain.Account, 0, len(ids))
	for _, id := range ids {
		if id <= t.lastLock {
			return nil, fmt.Errorf("%w: %q after %q", ErrLockOrder, id, t.lastLock)
		}
		ch := t.Repo.lockChan(id)
		if ch == nil {
			return nil, fmt.Errorf("%w: %s", domain.ErrAccountNotFound, id)
		}
		select {
		case ch <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		t.held[id] = true
		t.order = append(t.order, id)
		t.lastLock = id
		a, err := t.Repo.GetAccount(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (t *Tx) CreateAccount(_ context.Context, a domain.Account) error {
	t.newAccounts = append(t.newAccounts, a)
	return nil
}

func (t *Tx) UpdateAccount(_ context.Context, a domain.Account, expectedVersion int64) error {
	t.updates = append(t.updates, accountUpdate{a, expectedVersion})
	return nil
}

func (t *Tx) InsertJournal(_ context.Context, j domain.Journal) error {
	t.journals = append(t.journals, cloneJournal(j))
	return nil
}

func (t *Tx) InsertHold(_ context.Context, h domain.Hold) error {
	t.newHolds = append(t.newHolds, h)
	return nil
}

func (t *Tx) UpdateHold(_ context.Context, h domain.Hold) error {
	t.holdUpdates = append(t.holdUpdates, h)
	return nil
}

// commit validates everything first, then applies atomically.
func (r *Repo) commit(t *Tx) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	owners, codes, ids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, a := range t.newAccounts {
		_, byID := r.accounts[a.ID]
		_, byOwner := r.byOwner[ownerKey(a.OwnerRef, a.Currency)]
		_, byCode := r.byCode[a.Code]
		okOwner := a.Kind != domain.KindWallet || !(byOwner || owners[ownerKey(a.OwnerRef, a.Currency)])
		okCode := a.Kind != domain.KindSystem || !(byCode || codes[a.Code])
		if byID || ids[a.ID] || !okOwner || !okCode {
			return fmt.Errorf("%w: %s", domain.ErrAccountExists, a.ID)
		}
		ids[a.ID] = true
		owners[ownerKey(a.OwnerRef, a.Currency)] = true
		codes[a.Code] = true
	}
	for _, u := range t.updates {
		cur, ok := r.accounts[u.acct.ID]
		switch {
		case !ok:
			return fmt.Errorf("%w: %s", domain.ErrAccountNotFound, u.acct.ID)
		case !t.held[u.acct.ID]:
			return fmt.Errorf("%w: %s", ErrNotLocked, u.acct.ID)
		case cur.Version != u.version:
			return fmt.Errorf("%w: %s at v%d, expected v%d", domain.ErrConcurrentModification, u.acct.ID, cur.Version, u.version)
		case !u.acct.AllowNegative && u.acct.Balance-u.acct.Held < 0, u.acct.Held < 0:
			return fmt.Errorf("memory: account %s violates balance invariant", u.acct.ID)
		}
	}
	seenKeys, seenRev := map[string]bool{}, map[string]bool{}
	for _, j := range t.journals {
		if _, dup := r.journals[j.IdempotencyKey]; dup || seenKeys[j.IdempotencyKey] {
			return fmt.Errorf("%w: journal key %s", domain.ErrDuplicateKey, j.IdempotencyKey)
		}
		seenKeys[j.IdempotencyKey] = true
		if j.ReversesID != "" {
			if _, dup := r.reversed[j.ReversesID]; dup || seenRev[j.ReversesID] {
				return fmt.Errorf("%w: %s", domain.ErrAlreadyReversed, j.ReversesID)
			}
			seenRev[j.ReversesID] = true
		}
		d, c := j.Totals()
		if d != c {
			return fmt.Errorf("%w: journal %s", domain.ErrUnbalancedJournal, j.ID)
		}
	}
	seenRefs := map[string]bool{}
	for _, h := range t.newHolds {
		if _, dup := r.holds[h.Reference]; dup || seenRefs[h.Reference] {
			return fmt.Errorf("%w: hold ref %s", domain.ErrDuplicateKey, h.Reference)
		}
		seenRefs[h.Reference] = true
	}
	for _, h := range t.holdUpdates {
		prev, ok := r.holds[h.Reference]
		if !ok || prev.Status != domain.HoldActive {
			return fmt.Errorf("%w: %s", domain.ErrHoldNotActive, h.Reference)
		}
	}

	for _, a := range t.newAccounts {
		r.accounts[a.ID] = a
		r.locks[a.ID] = make(chan struct{}, 1)
		if a.Kind == domain.KindWallet {
			r.byOwner[ownerKey(a.OwnerRef, a.Currency)] = a.ID
		} else {
			r.byCode[a.Code] = a.ID
		}
	}
	for _, u := range t.updates {
		r.accounts[u.acct.ID] = u.acct
	}
	for _, j := range t.journals {
		r.journals[j.IdempotencyKey] = j
		if j.ReversesID != "" {
			r.reversed[j.ReversesID] = j.IdempotencyKey
		}
		for _, e := range j.Entries {
			r.seq++
			r.stmts[e.AccountID] = append(r.stmts[e.AccountID], domain.StatementLine{
				Seq: r.seq, JournalID: j.ID, Kind: j.Kind, Reference: j.Reference, Description: j.Description,
				Side: e.Side, Amount: e.Amount, BalanceAfter: e.BalanceAfter, PostedAt: j.PostedAt,
			})
			s := r.sums[e.AccountID]
			if e.Side == domain.Debit {
				s.debits += e.Amount.Minor
			} else {
				s.credits += e.Amount.Minor
			}
			r.sums[e.AccountID] = s
		}
	}
	for _, h := range t.newHolds {
		r.holds[h.Reference] = h
	}
	for _, h := range t.holdUpdates {
		r.holds[h.Reference] = h
	}
	return nil
}
