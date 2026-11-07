package app

import (
	"context"

	"github.com/codercollo/nanga/internal/ledger/domain"
	"github.com/codercollo/nanga/internal/platform/money"
)

// Codes of the v1 chart of accounts (spec 4.2). Customer wallets are created
// per user with CreateWallet.
const (
	AccountMpesaClearing = "mpesa_clearing"
	AccountMpesaFloat    = "mpesa_float"
	AccountFeesRevenue   = "fees_revenue"
	AccountSuspense      = "suspense"
)

// StandardChart is the v1 set of system accounts. Assets are debit-normal and
// may not go negative; revenue is credit-normal; suspense (unmatched or
// in-doubt funds) may be positive or negative until reconciled.
var StandardChart = []SystemAccountSpec{
	{Code: AccountMpesaClearing, Currency: money.KES, Normal: domain.Debit},
	{Code: AccountMpesaFloat, Currency: money.KES, Normal: domain.Debit},
	{Code: AccountFeesRevenue, Currency: money.KES, Normal: domain.Credit},
	{Code: AccountSuspense, Currency: money.KES, Normal: domain.Credit, AllowNegative: true},
}

// BootstrapChart creates any missing system accounts and returns all of them
// keyed by code. Safe to run on every startup.
func (s *Service) BootstrapChart(ctx context.Context) (map[string]domain.Account, error) {
	out := make(map[string]domain.Account, len(StandardChart))
	for _, spec := range StandardChart {
		a, err := s.EnsureSystemAccount(ctx, spec)
		if err != nil {
			return nil, err
		}
		out[spec.Code] = a
	}
	return out, nil
}
