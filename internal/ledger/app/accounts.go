package app

import (
	"context"
	"errors"

	"github.com/codercollo/nanga/internal/ledger/domain"
	"github.com/codercollo/nanga/internal/platform/money"
)

// BalanceView is the read model for a balance query.
type BalanceView struct {
	AccountID string
	Balance   money.Money
	Held      money.Money
	Available money.Money
}

func view(a domain.Account) BalanceView {
	return BalanceView{
		AccountID: a.ID,
		Balance:   a.BalanceMoney(),
		Held:      money.Money{Minor: a.Held, Currency: a.Currency},
		Available: a.AvailableMoney(),
	}
}

// CreateWallet returns the wallet for (ownerRef, currency), creating it when
// absent. Safe to call concurrently and repeatedly.
func (s *Service) CreateWallet(ctx context.Context, ownerRef string, cur money.Currency) (domain.Account, error) {
	var out domain.Account
	err := s.inTx(ctx, func(ctx context.Context, tx Tx) error {
		existing, err := tx.GetAccountByOwner(ctx, ownerRef, cur)
		if err == nil {
			out = existing
			return nil
		}
		if !errors.Is(err, domain.ErrAccountNotFound) {
			return err
		}
		w, err := domain.NewWallet(s.ids.NewID(), ownerRef, cur, s.clock.Now())
		if err != nil {
			return err
		}
		if err := tx.CreateAccount(ctx, w); err != nil {
			return err
		}
		out = w
		return nil
	})
	return out, err
}

// SystemAccountSpec describes an internal account.
type SystemAccountSpec struct {
	Code          string
	Currency      money.Currency
	Normal        domain.Side
	AllowNegative bool
}

// EnsureSystemAccount returns the system account with the given code,
// creating it when absent. An existing account whose definition differs from
// the spec is reported as an error rather than silently reused.
func (s *Service) EnsureSystemAccount(ctx context.Context, spec SystemAccountSpec) (domain.Account, error) {
	var out domain.Account
	err := s.inTx(ctx, func(ctx context.Context, tx Tx) error {
		existing, err := tx.GetAccountByCode(ctx, spec.Code)
		if err == nil {
			if existing.Currency != spec.Currency || existing.Normal != spec.Normal || existing.AllowNegative != spec.AllowNegative {
				return domain.ErrAccountExists
			}
			out = existing
			return nil
		}
		if !errors.Is(err, domain.ErrAccountNotFound) {
			return err
		}
		a, err := domain.NewSystemAccount(s.ids.NewID(), spec.Code, spec.Currency, spec.Normal, spec.AllowNegative, s.clock.Now())
		if err != nil {
			return err
		}
		if err := tx.CreateAccount(ctx, a); err != nil {
			return err
		}
		out = a
		return nil
	})
	return out, err
}

// GetAccount returns an account by id.
func (s *Service) GetAccount(ctx context.Context, id string) (domain.Account, error) {
	ctx, cancel := s.bound(ctx)
	defer cancel()
	return s.repo.GetAccount(ctx, id)
}

// GetBalance returns balance, held and available funds for an account.
func (s *Service) GetBalance(ctx context.Context, accountID string) (BalanceView, error) {
	ctx, cancel := s.bound(ctx)
	defer cancel()
	a, err := s.repo.GetAccount(ctx, accountID)
	if err != nil {
		return BalanceView{}, err
	}
	return view(a), nil
}

// GetWalletBalance returns the balance view of a user's wallet.
func (s *Service) GetWalletBalance(ctx context.Context, ownerRef string, cur money.Currency) (BalanceView, error) {
	ctx, cancel := s.bound(ctx)
	defer cancel()
	a, err := s.repo.GetAccountByOwner(ctx, ownerRef, cur)
	if err != nil {
		return BalanceView{}, err
	}
	return view(a), nil
}
