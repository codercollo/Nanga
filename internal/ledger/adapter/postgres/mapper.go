package postgres

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/codercollo/nanga/internal/ledger/adapter/postgres/sqlcgen"
	"github.com/codercollo/nanga/internal/ledger/domain"
	"github.com/codercollo/nanga/internal/platform/money"
)

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optTime(h domain.Hold) *time.Time {
	if h.ResolvedAt.IsZero() {
		return nil
	}
	t := h.ResolvedAt
	return &t
}

func toAccount(r sqlcgen.LedgerAccount) domain.Account {
	return domain.Account{
		ID: r.ID, Kind: domain.AccountKind(r.Kind), Code: r.Code.String, OwnerRef: r.OwnerRef.String,
		Currency: money.Currency(r.Currency), Normal: domain.Side(r.NormalSide), AllowNegative: r.AllowNegative,
		Status: domain.AccountStatus(r.Status), Balance: r.Balance, Held: r.Held, Version: r.Version,
		CreatedAt: r.CreatedAt,
	}
}

func toJournal(r sqlcgen.LedgerJournal, lines []sqlcgen.LedgerEntry) domain.Journal {
	entries := make([]domain.Entry, len(lines))
	for i, l := range lines {
		entries[i] = domain.Entry{
			AccountID: l.AccountID, Side: domain.Side(l.Side),
			Amount:       money.Money{Minor: l.Amount, Currency: money.Currency(l.Currency)},
			BalanceAfter: l.BalanceAfter,
		}
	}
	j := domain.Journal{
		ID: r.ID, IdempotencyKey: r.IdempotencyKey, Fingerprint: r.Fingerprint, Kind: r.Kind,
		Reference: r.Reference, Description: r.Description, Currency: money.Currency(r.Currency),
		Entries: entries, PostedAt: r.PostedAt,
	}
	if r.ReversesID != nil {
		j.ReversesID = *r.ReversesID
	}
	return j
}

func toHold(r sqlcgen.LedgerHold) domain.Hold {
	h := domain.Hold{
		ID: r.ID, AccountID: r.AccountID, Reference: r.Reference,
		Amount: money.Money{Minor: r.Amount, Currency: money.Currency(r.Currency)},
		Status: domain.HoldStatus(r.Status), CreatedAt: r.CreatedAt,
	}
	if r.JournalID != nil {
		h.JournalID = *r.JournalID
	}
	if r.ResolvedAt != nil {
		h.ResolvedAt = *r.ResolvedAt
	}
	return h
}
