package app

import (
	"context"
	"fmt"

	"github.com/codercollo/nanga/internal/ledger/domain"
)

// TrialBalance returns total debits and credits per currency. Every line must
// be balanced; reconciliation alerts when it is not.
func (s *Service) TrialBalance(ctx context.Context) ([]domain.TrialBalanceLine, error) {
	return s.repo.TrialBalance(ctx)
}

// VerifyAccount recomputes the account balance from its entries and compares
// it with the stored balance.
func (s *Service) VerifyAccount(ctx context.Context, accountID string) error {
	a, err := s.repo.GetAccount(ctx, accountID)
	if err != nil {
		return err
	}
	return verify(ctx, s.repo, a)
}

func verify(ctx context.Context, r Reader, a domain.Account) error {
	d, c, err := r.EntryTotals(ctx, a.ID)
	if err != nil {
		return err
	}
	want := c - d
	if a.Normal == domain.Debit {
		want = d - c
	}
	if a.Balance != want {
		return fmt.Errorf("%w: account %s stored %d, entries imply %d", domain.ErrBalanceMismatch, a.ID, a.Balance, want)
	}
	return nil
}

// VerifyLedger checks that every trial-balance line is balanced and that every
// account's stored balance equals the sum of its entries. It returns the
// number of accounts checked.
func (s *Service) VerifyLedger(ctx context.Context) (int, error) {
	lines, err := s.repo.TrialBalance(ctx)
	if err != nil {
		return 0, err
	}
	for _, l := range lines {
		if !l.Balanced() {
			return 0, fmt.Errorf("%w: trial balance %s debits %d != credits %d", domain.ErrUnbalancedJournal, l.Currency, l.Debits, l.Credits)
		}
	}
	checked, after := 0, ""
	for {
		page, err := s.repo.ListAccounts(ctx, after, 500)
		if err != nil {
			return checked, err
		}
		for _, a := range page {
			if err := verify(ctx, s.repo, a); err != nil {
				return checked, err
			}
			checked++
		}
		if len(page) < 500 {
			return checked, nil
		}
		after = page[len(page)-1].ID
	}
}
