// Package postgres is the PostgreSQL ledger repository. It uses sqlc-generated
// queries (db/queries/ledger) over pgx and maps database errors to domain
// errors. Correctness under concurrency comes from per-account row locks taken
// in ascending id order; CHECK constraints and triggers are a backstop.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/codercollo/nanga/internal/ledger/adapter/postgres/sqlcgen"
	"github.com/codercollo/nanga/internal/ledger/app"
	"github.com/codercollo/nanga/internal/ledger/domain"
	"github.com/codercollo/nanga/internal/platform/money"
	"github.com/codercollo/nanga/internal/platform/pg"
)

var (
	_ app.Repository = (*Repo)(nil)
	_ app.Tx         = (*Tx)(nil)
)

// ErrLockOrder is returned when accounts are locked out of ascending order.
var ErrLockOrder = errors.New("postgres: accounts must be locked in ascending id order")

// Repo implements app.Repository on a pgx pool.
type Repo struct {
	reader
	pool *pgxpool.Pool
}

// New returns a repository over pool.
func New(pool *pgxpool.Pool) *Repo {
	return &Repo{reader: reader{q: sqlcgen.New(pool)}, pool: pool}
}

// WithTx runs fn in a READ COMMITTED transaction (row locks provide the
// serialisation; see LockAccounts). fn may be re-run on deadlock/serialization
// failures, which the service layer's operations tolerate.
func (r *Repo) WithTx(ctx context.Context, fn func(ctx context.Context, tx app.Tx) error) error {
	return pg.WithTx(ctx, r.pool, pgx.TxOptions{}, func(t pgx.Tx) error {
		return fn(ctx, &Tx{reader: reader{q: sqlcgen.New(t)}})
	})
}

// reader holds the read queries shared by Repo (pool) and Tx (transaction).
type reader struct{ q *sqlcgen.Queries }

func (r reader) GetAccount(ctx context.Context, id string) (domain.Account, error) {
	row, err := r.q.GetAccount(ctx, id)
	return accountResult(row, err, id)
}

func (r reader) GetAccountByOwner(ctx context.Context, owner string, cur money.Currency) (domain.Account, error) {
	row, err := r.q.GetWalletByOwner(ctx, sqlcgen.GetWalletByOwnerParams{OwnerRef: pgtype.Text{String: owner, Valid: true}, Currency: string(cur)})
	return accountResult(row, err, "owner "+owner)
}

func (r reader) GetAccountByCode(ctx context.Context, code string) (domain.Account, error) {
	row, err := r.q.GetAccountByCode(ctx, pgtype.Text{String: code, Valid: true})
	return accountResult(row, err, "code "+code)
}

func (r reader) ListAccounts(ctx context.Context, afterID string, limit int) ([]domain.Account, error) {
	if afterID == "" {
		afterID = "00000000-0000-0000-0000-000000000000"
	}
	rows, err := r.q.ListAccounts(ctx, sqlcgen.ListAccountsParams{ID: afterID, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Account, len(rows))
	for i, row := range rows {
		out[i] = toAccount(row)
	}
	return out, nil
}

func (r reader) GetJournalByKey(ctx context.Context, key string) (domain.Journal, error) {
	row, err := r.q.GetJournalByKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Journal{}, fmt.Errorf("%w: key %s", domain.ErrJournalNotFound, key)
	}
	if err != nil {
		return domain.Journal{}, err
	}
	lines, err := r.q.ListJournalEntries(ctx, row.ID)
	if err != nil {
		return domain.Journal{}, err
	}
	return toJournal(row, lines), nil
}

func (r reader) GetReversalOf(ctx context.Context, journalID string) (domain.Journal, error) {
	row, err := r.q.GetReversalOf(ctx, &journalID)
	if errors.Is(err, pgx.ErrNoRows) || pg.InvalidText(err) {
		return domain.Journal{}, fmt.Errorf("%w: no reversal of %s", domain.ErrJournalNotFound, journalID)
	}
	if err != nil {
		return domain.Journal{}, err
	}
	lines, err := r.q.ListJournalEntries(ctx, row.ID)
	if err != nil {
		return domain.Journal{}, err
	}
	return toJournal(row, lines), nil
}

func (r reader) ListStatement(ctx context.Context, accountID string, beforeSeq int64, limit int) ([]domain.StatementLine, error) {
	if beforeSeq <= 0 {
		beforeSeq = math.MaxInt64
	}
	rows, err := r.q.ListAccountStatement(ctx, sqlcgen.ListAccountStatementParams{AccountID: accountID, Seq: beforeSeq, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]domain.StatementLine, len(rows))
	for i, x := range rows {
		out[i] = domain.StatementLine{
			Seq: x.Seq, JournalID: x.JournalID, Kind: x.Kind, Reference: x.Reference, Description: x.Description,
			Side: domain.Side(x.Side), Amount: money.Money{Minor: x.Amount, Currency: money.Currency(x.Currency)},
			BalanceAfter: x.BalanceAfter, PostedAt: x.PostedAt,
		}
	}
	return out, nil
}

func (r reader) GetHoldByRef(ctx context.Context, ref string) (domain.Hold, error) {
	row, err := r.q.GetHoldByRef(ctx, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Hold{}, fmt.Errorf("%w: %s", domain.ErrHoldNotFound, ref)
	}
	if err != nil {
		return domain.Hold{}, err
	}
	return toHold(row), nil
}

func (r reader) EntryTotals(ctx context.Context, accountID string) (int64, int64, error) {
	row, err := r.q.EntryTotalsForAccount(ctx, accountID)
	if err != nil {
		return 0, 0, err
	}
	return row.Debits, row.Credits, nil
}

func (r reader) TrialBalance(ctx context.Context) ([]domain.TrialBalanceLine, error) {
	rows, err := r.q.TrialBalance(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.TrialBalanceLine, len(rows))
	for i, row := range rows {
		out[i] = domain.TrialBalanceLine{Currency: money.Currency(row.Currency), Debits: row.Debits, Credits: row.Credits}
	}
	return out, nil
}

func accountResult(row sqlcgen.LedgerAccount, err error, what string) (domain.Account, error) {
	if errors.Is(err, pgx.ErrNoRows) || pg.InvalidText(err) {
		return domain.Account{}, fmt.Errorf("%w: %s", domain.ErrAccountNotFound, what)
	}
	if err != nil {
		return domain.Account{}, err
	}
	return toAccount(row), nil
}

// Tx implements app.Tx inside a pgx transaction.
type Tx struct {
	reader
	lastLock string
}

// LockAccounts takes FOR UPDATE row locks one account at a time, in the order
// given (which must be ascending). Locking row by row, rather than with one
// ORDER BY ... FOR UPDATE query, guarantees the acquisition order.
func (t *Tx) LockAccounts(ctx context.Context, ids []string) ([]domain.Account, error) {
	out := make([]domain.Account, 0, len(ids))
	for _, id := range ids {
		if id <= t.lastLock {
			return nil, fmt.Errorf("%w: %q after %q", ErrLockOrder, id, t.lastLock)
		}
		row, err := t.q.GetAccountForUpdate(ctx, id)
		a, err := accountResult(row, err, id)
		if err != nil {
			return nil, err
		}
		t.lastLock = id
		out = append(out, a)
	}
	return out, nil
}

func (t *Tx) CreateAccount(ctx context.Context, a domain.Account) error {
	err := t.q.InsertAccount(ctx, sqlcgen.InsertAccountParams{
		ID: a.ID, Kind: string(a.Kind), Code: text(a.Code), OwnerRef: text(a.OwnerRef),
		Currency: string(a.Currency), NormalSide: string(a.Normal), AllowNegative: a.AllowNegative,
		Status: string(a.Status), Balance: a.Balance, Held: a.Held, Version: a.Version, CreatedAt: a.CreatedAt,
	})
	return mapWriteErr(err)
}

func (t *Tx) UpdateAccount(ctx context.Context, a domain.Account, expectedVersion int64) error {
	n, err := t.q.UpdateAccountState(ctx, sqlcgen.UpdateAccountStateParams{
		ID: a.ID, Balance: a.Balance, Held: a.Held, Status: string(a.Status),
		Version: a.Version, Version_2: expectedVersion,
	})
	if err != nil {
		return mapWriteErr(err)
	}
	if n != 1 {
		return fmt.Errorf("%w: account %s expected v%d", domain.ErrConcurrentModification, a.ID, expectedVersion)
	}
	return nil
}

func (t *Tx) InsertJournal(ctx context.Context, j domain.Journal) error {
	err := t.q.InsertJournal(ctx, sqlcgen.InsertJournalParams{
		ID: j.ID, IdempotencyKey: j.IdempotencyKey, Fingerprint: j.Fingerprint, Kind: j.Kind,
		Reference: j.Reference, Description: j.Description, Currency: string(j.Currency), PostedAt: j.PostedAt,
		ReversesID: optString(j.ReversesID),
	})
	if err != nil {
		return mapWriteErr(err)
	}
	for i, e := range j.Entries {
		err := t.q.InsertEntry(ctx, sqlcgen.InsertEntryParams{
			JournalID: j.ID, LineNo: int32(i), AccountID: e.AccountID, Side: string(e.Side),
			Amount: e.Amount.Minor, Currency: string(e.Amount.Currency), BalanceAfter: e.BalanceAfter,
		})
		if err != nil {
			return mapWriteErr(err)
		}
	}
	return nil
}

func (t *Tx) InsertHold(ctx context.Context, h domain.Hold) error {
	err := t.q.InsertHold(ctx, sqlcgen.InsertHoldParams{
		ID: h.ID, AccountID: h.AccountID, Reference: h.Reference, Amount: h.Amount.Minor,
		Currency: string(h.Amount.Currency), Status: string(h.Status), CreatedAt: h.CreatedAt,
		JournalID: optString(h.JournalID), ResolvedAt: optTime(h),
	})
	return mapWriteErr(err)
}

func (t *Tx) UpdateHold(ctx context.Context, h domain.Hold) error {
	n, err := t.q.ResolveHold(ctx, sqlcgen.ResolveHoldParams{
		Reference: h.Reference, Status: string(h.Status), JournalID: optString(h.JournalID), ResolvedAt: optTime(h),
	})
	if err != nil {
		return mapWriteErr(err)
	}
	if n != 1 {
		return fmt.Errorf("%w: %s", domain.ErrHoldNotActive, h.Reference)
	}
	return nil
}

// mapWriteErr translates constraint violations into domain errors.
func mapWriteErr(err error) error {
	if err == nil {
		return nil
	}
	if c, ok := pg.UniqueViolation(err); ok {
		if c == "journals_reverses_id_key" {
			return fmt.Errorf("%w: %s", domain.ErrAlreadyReversed, c)
		}
		if strings.HasPrefix(c, "accounts_") {
			return fmt.Errorf("%w: %s", domain.ErrAccountExists, c)
		}
		return fmt.Errorf("%w: %s", domain.ErrDuplicateKey, c)
	}
	if c, ok := pg.CheckViolation(err); ok && c == "accounts_non_negative" {
		return fmt.Errorf("%w: database backstop (%s)", domain.ErrInsufficientFunds, c)
	}
	return err
}
