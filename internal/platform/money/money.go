// Package money provides the Money value object: integer minor units plus a
// currency code. Floats are never used.
package money

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Currency is an ISO-4217 code.
type Currency string

// KES is the Kenyan shilling, the only currency supported today.
const KES Currency = "KES"

var exponents = map[Currency]int{KES: 2}

var (
	ErrInvalidCurrency  = errors.New("money: unsupported currency")
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	ErrOverflow         = errors.New("money: arithmetic overflow")
	ErrInvalidAmount    = errors.New("money: invalid amount")
)

// Valid reports whether the currency is supported.
func (c Currency) Valid() bool { _, ok := exponents[c]; return ok }

// Exponent is the number of minor-unit decimal places (2 for KES).
func (c Currency) Exponent() int { return exponents[c] }

// Money is an amount in minor units (cents) of a currency.
type Money struct {
	Minor    int64
	Currency Currency
}

// New validates the currency and returns Money.
func New(minor int64, c Currency) (Money, error) {
	if !c.Valid() {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, string(c))
	}
	return Money{Minor: minor, Currency: c}, nil
}

// Zero returns zero in the given currency.
func Zero(c Currency) Money { return Money{Currency: c} }

// FromKES builds a KES amount from minor units.
func FromKES(minor int64) Money { return Money{Minor: minor, Currency: KES} }

func (m Money) IsZero() bool     { return m.Minor == 0 }
func (m Money) IsPositive() bool { return m.Minor > 0 }
func (m Money) IsNegative() bool { return m.Minor < 0 }

func (m Money) same(o Money) error {
	if m.Currency != o.Currency {
		return fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.Currency, o.Currency)
	}
	return nil
}

// Add returns m+o, failing on currency mismatch or overflow.
func (m Money) Add(o Money) (Money, error) {
	if err := m.same(o); err != nil {
		return Money{}, err
	}
	if (o.Minor > 0 && m.Minor > math.MaxInt64-o.Minor) || (o.Minor < 0 && m.Minor < math.MinInt64-o.Minor) {
		return Money{}, ErrOverflow
	}
	return Money{Minor: m.Minor + o.Minor, Currency: m.Currency}, nil
}

// Sub returns m-o, failing on currency mismatch or overflow.
func (m Money) Sub(o Money) (Money, error) {
	if err := m.same(o); err != nil {
		return Money{}, err
	}
	if (o.Minor < 0 && m.Minor > math.MaxInt64+o.Minor) || (o.Minor > 0 && m.Minor < math.MinInt64+o.Minor) {
		return Money{}, ErrOverflow
	}
	return Money{Minor: m.Minor - o.Minor, Currency: m.Currency}, nil
}

// Neg returns -m.
func (m Money) Neg() (Money, error) {
	if m.Minor == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{Minor: -m.Minor, Currency: m.Currency}, nil
}

// Cmp returns -1, 0 or 1.
func (m Money) Cmp(o Money) (int, error) {
	if err := m.same(o); err != nil {
		return 0, err
	}
	switch {
	case m.Minor < o.Minor:
		return -1, nil
	case m.Minor > o.Minor:
		return 1, nil
	}
	return 0, nil
}

func pow10(n int) uint64 {
	p := uint64(1)
	for i := 0; i < n; i++ {
		p *= 10
	}
	return p
}

// String renders e.g. "KES 1,234.50" or "-KES 5.00".
func (m Money) String() string {
	exp := m.Currency.Exponent()
	abs := uint64(m.Minor)
	sign := ""
	if m.Minor < 0 {
		abs = -uint64(m.Minor) // two's complement handles MinInt64
		sign = "-"
	}
	scale := pow10(exp)
	whole := strconv.FormatUint(abs/scale, 10)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := fmt.Sprintf("%s%s %s", sign, m.Currency, b.String())
	if exp > 0 {
		out += fmt.Sprintf(".%0*d", exp, abs%scale)
	}
	return out
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ParseMajor parses a decimal major-unit string ("1234.50") into Money
// without going through floating point.
func ParseMajor(s string, c Currency) (Money, error) {
	if !c.Valid() {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, string(c))
	}
	exp := c.Exponent()
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, hasDot := strings.Cut(s, ".")
	bad := fmt.Errorf("%w: %q", ErrInvalidAmount, s)
	if !allDigits(intPart) || (hasDot && (!allDigits(frac) || len(frac) > exp)) {
		return Money{}, bad
	}
	frac += strings.Repeat("0", exp-len(frac))
	whole, err := strconv.ParseUint(intPart, 10, 63)
	if err != nil {
		return Money{}, ErrOverflow
	}
	var fracVal uint64
	if exp > 0 {
		fracVal, _ = strconv.ParseUint(frac, 10, 63)
	}
	scale := pow10(exp)
	if whole > math.MaxInt64/scale || whole*scale > math.MaxInt64-fracVal {
		return Money{}, ErrOverflow
	}
	minor := int64(whole*scale + fracVal)
	if neg {
		minor = -minor
	}
	return Money{Minor: minor, Currency: c}, nil
}
