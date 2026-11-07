package domain

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"time"

	"github.com/codercollo/nanga/internal/platform/money"
)

// Side of a double-entry line.
type Side string

const (
	Debit  Side = "debit"
	Credit Side = "credit"
)

// Valid reports whether s is a known side.
func (s Side) Valid() bool { return s == Debit || s == Credit }

// Opposite returns the other side.
func (s Side) Opposite() Side {
	if s == Debit {
		return Credit
	}
	return Debit
}

// AccountKind distinguishes customer wallets from internal system accounts.
type AccountKind string

const (
	KindWallet AccountKind = "wallet"
	KindSystem AccountKind = "system"
)

// AccountStatus gates postings.
type AccountStatus string

const (
	StatusActive AccountStatus = "active"
	StatusFrozen AccountStatus = "frozen"
	StatusClosed AccountStatus = "closed"
)

var codeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

// Account is a ledger account. Balance is stored in the account's normal
// side convention: a credit-normal account (customer wallet, liability) grows
// with credits; a debit-normal account (asset such as M-Pesa clearing) grows
// with debits. Balance and Held are minor units of Currency.
type Account struct {
	ID            string
	Kind          AccountKind
	Code          string // system accounts only
	OwnerRef      string // wallets only (identity user id)
	Currency      money.Currency
	Normal        Side
	AllowNegative bool
	Status        AccountStatus
	Balance       int64
	Held          int64
	Version       int64
	CreatedAt     time.Time
}

// NewWallet creates a customer wallet: credit-normal, never negative.
func NewWallet(id, ownerRef string, cur money.Currency, now time.Time) (Account, error) {
	if id == "" || ownerRef == "" || len(ownerRef) > 64 {
		return Account{}, invalid("wallet requires id and owner_ref (<=64 chars)")
	}
	if !cur.Valid() {
		return Account{}, invalid("unsupported currency %q", cur)
	}
	return Account{ID: id, Kind: KindWallet, OwnerRef: ownerRef, Currency: cur, Normal: Credit,
		Status: StatusActive, CreatedAt: now}, nil
}

// NewSystemAccount creates an internal account (e.g. mpesa_clearing).
func NewSystemAccount(id, code string, cur money.Currency, normal Side, allowNegative bool, now time.Time) (Account, error) {
	if id == "" || !codeRe.MatchString(code) {
		return Account{}, invalid("system account requires id and code matching %s", codeRe)
	}
	if !cur.Valid() {
		return Account{}, invalid("unsupported currency %q", cur)
	}
	if !normal.Valid() {
		return Account{}, invalid("invalid normal side %q", normal)
	}
	return Account{ID: id, Kind: KindSystem, Code: code, Currency: cur, Normal: normal,
		AllowNegative: allowNegative, Status: StatusActive, CreatedAt: now}, nil
}

// Available is Balance minus funds reserved by active holds.
func (a Account) Available() int64 { return a.Balance - a.Held }

// BalanceMoney returns Balance as Money.
func (a Account) BalanceMoney() money.Money {
	return money.Money{Minor: a.Balance, Currency: a.Currency}
}

// AvailableMoney returns Available as Money.
func (a Account) AvailableMoney() money.Money {
	return money.Money{Minor: a.Available(), Currency: a.Currency}
}

func addChecked(x, y int64) (int64, bool) {
	if (y > 0 && x > math.MaxInt64-y) || (y < 0 && x < math.MinInt64-y) {
		return 0, false
	}
	return x + y, true
}

// checkInvariant enforces the non-negative rule.
func (a Account) checkInvariant() error {
	if !a.AllowNegative && a.Balance-a.Held < 0 {
		return fmt.Errorf("%w: account %s available %d", ErrInsufficientFunds, a.ID, a.Balance-a.Held)
	}
	return nil
}

// ApplyEntries applies all entries (which must belong to this account) in
// order and returns the updated account plus the balance after each entry.
// The non-negative invariant is checked once, after the whole group, so
// intra-journal ordering cannot cause false rejections.
func (a Account) ApplyEntries(entries []Entry) (Account, []int64, error) {
	if a.Status != StatusActive {
		return Account{}, nil, fmt.Errorf("%w: %s is %s", ErrAccountNotActive, a.ID, a.Status)
	}
	if len(entries) == 0 {
		return Account{}, nil, invalid("no entries for account %s", a.ID)
	}
	out := a
	after := make([]int64, len(entries))
	for i, e := range entries {
		if e.AccountID != a.ID {
			return Account{}, nil, invalid("entry for %s applied to %s", e.AccountID, a.ID)
		}
		if e.Amount.Currency != a.Currency {
			return Account{}, nil, fmt.Errorf("%w: entry %s vs account %s", ErrCurrencyMismatch, e.Amount.Currency, a.Currency)
		}
		if !e.Amount.IsPositive() || !e.Side.Valid() {
			return Account{}, nil, invalid("entry amount must be positive with a valid side")
		}
		delta := e.Amount.Minor
		if e.Side != a.Normal {
			delta = -delta
		}
		nb, ok := addChecked(out.Balance, delta)
		if !ok {
			return Account{}, nil, ErrOverflow
		}
		out.Balance = nb
		after[i] = nb
	}
	if err := out.checkInvariant(); err != nil {
		return Account{}, nil, err
	}
	out.Version++
	return out, after, nil
}

// PlaceHold reserves amount from the available balance.
func (a Account) PlaceHold(amount money.Money) (Account, error) {
	if a.Status != StatusActive {
		return Account{}, fmt.Errorf("%w: %s is %s", ErrAccountNotActive, a.ID, a.Status)
	}
	if amount.Currency != a.Currency {
		return Account{}, fmt.Errorf("%w: hold %s vs account %s", ErrCurrencyMismatch, amount.Currency, a.Currency)
	}
	if !amount.IsPositive() {
		return Account{}, invalid("hold amount must be positive")
	}
	if a.Available() < amount.Minor {
		return Account{}, fmt.Errorf("%w: account %s available %d < hold %d", ErrInsufficientFunds, a.ID, a.Available(), amount.Minor)
	}
	a.Held += amount.Minor
	a.Version++
	return a, nil
}

// ReleaseHold returns amount to the available balance. It is allowed on
// non-active accounts so frozen accounts can still resolve holds.
func (a Account) ReleaseHold(amount money.Money) (Account, error) {
	if amount.Currency != a.Currency {
		return Account{}, fmt.Errorf("%w: hold %s vs account %s", ErrCurrencyMismatch, amount.Currency, a.Currency)
	}
	if !amount.IsPositive() || amount.Minor > a.Held {
		return Account{}, invalid("release %d exceeds held %d", amount.Minor, a.Held)
	}
	a.Held -= amount.Minor
	a.Version++
	return a, nil
}

// SortedUniqueIDs returns ids sorted ascending without duplicates. Every
// multi-account write must lock accounts in exactly this order to make
// deadlocks impossible.
func SortedUniqueIDs(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	n := 0
	for i, s := range out {
		if i == 0 || s != out[i-1] {
			out[n] = s
			n++
		}
	}
	return out[:n]
}
