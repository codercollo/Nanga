package domain

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/codercollo/nanga/internal/platform/money"
)

var now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func wallet(t *testing.T, id string, balance int64) Account {
	t.Helper()
	a, err := NewWallet(id, "user-"+id, money.KES, now)
	if err != nil {
		t.Fatal(err)
	}
	a.Balance = balance
	return a
}

func entry(acc string, s Side, minor int64) Entry {
	return Entry{AccountID: acc, Side: s, Amount: money.FromKES(minor)}
}

func TestNewWalletAndSystem(t *testing.T) {
	w, err := NewWallet("a", "u1", money.KES, now)
	if err != nil || w.Normal != Credit || w.AllowNegative || w.Status != StatusActive {
		t.Fatalf("wallet: %+v %v", w, err)
	}
	if _, err := NewWallet("", "u", money.KES, now); !errors.Is(err, ErrInvalid) {
		t.Fatal("want invalid for empty id")
	}
	if _, err := NewWallet("a", "u", "USD", now); !errors.Is(err, ErrInvalid) {
		t.Fatal("want invalid for currency")
	}
	if _, err := NewSystemAccount("s", "mpesa_clearing", money.KES, Debit, true, now); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"", "Bad", "1x", "a b", "x"} {
		if _, err := NewSystemAccount("s", code, money.KES, Debit, true, now); !errors.Is(err, ErrInvalid) {
			t.Errorf("code %q should be invalid", code)
		}
	}
	if _, err := NewSystemAccount("s", "ok_code", money.KES, "sideways", true, now); !errors.Is(err, ErrInvalid) {
		t.Fatal("bad side should be invalid")
	}
}

func TestApplyEntriesNormalSide(t *testing.T) {
	w := wallet(t, "a", 1000) // credit-normal
	up, after, err := w.ApplyEntries([]Entry{entry("a", Credit, 500)})
	if err != nil || up.Balance != 1500 || after[0] != 1500 || up.Version != 1 {
		t.Fatalf("credit: %+v %v %v", up, after, err)
	}
	up, _, err = w.ApplyEntries([]Entry{entry("a", Debit, 400)})
	if err != nil || up.Balance != 600 {
		t.Fatalf("debit: %+v %v", up, err)
	}
	asset, _ := NewSystemAccount("s", "float", money.KES, Debit, false, now)
	up, _, err = asset.ApplyEntries([]Entry{entry("s", Debit, 700)})
	if err != nil || up.Balance != 700 {
		t.Fatalf("debit-normal: %+v %v", up, err)
	}
}

func TestApplyEntriesDoesNotMutateReceiver(t *testing.T) {
	w := wallet(t, "a", 1000)
	_, _, _ = w.ApplyEntries([]Entry{entry("a", Debit, 100)})
	if w.Balance != 1000 || w.Version != 0 {
		t.Fatalf("receiver mutated: %+v", w)
	}
}

func TestInsufficientFundsAndHolds(t *testing.T) {
	w := wallet(t, "a", 1000)
	if _, _, err := w.ApplyEntries([]Entry{entry("a", Debit, 1001)}); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("want insufficient, got %v", err)
	}
	if _, _, err := w.ApplyEntries([]Entry{entry("a", Debit, 1000)}); err != nil {
		t.Fatalf("exact balance must pass: %v", err)
	}
	h, err := w.PlaceHold(money.FromKES(800))
	if err != nil || h.Held != 800 || h.Available() != 200 {
		t.Fatalf("hold: %+v %v", h, err)
	}
	if _, _, err := h.ApplyEntries([]Entry{entry("a", Debit, 201)}); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("held funds must be unspendable: %v", err)
	}
	if _, err := h.PlaceHold(money.FromKES(201)); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("over-hold must fail: %v", err)
	}
}

func TestInvariantCheckedOncePerGroup(t *testing.T) {
	w := wallet(t, "a", 100)
	// debit 150 then credit 100 in one journal: transiently negative, finally 50.
	up, after, err := w.ApplyEntries([]Entry{entry("a", Debit, 150), entry("a", Credit, 100)})
	if err != nil || up.Balance != 50 || !reflect.DeepEqual(after, []int64{-50, 50}) {
		t.Fatalf("got %+v %v %v", up, after, err)
	}
}

func TestAllowNegativeSystemAccount(t *testing.T) {
	s, _ := NewSystemAccount("s", "suspense", money.KES, Credit, true, now)
	up, _, err := s.ApplyEntries([]Entry{entry("s", Debit, 50)})
	if err != nil || up.Balance != -50 {
		t.Fatalf("%+v %v", up, err)
	}
}

func TestNotActive(t *testing.T) {
	w := wallet(t, "a", 100)
	w.Status = StatusFrozen
	if _, _, err := w.ApplyEntries([]Entry{entry("a", Credit, 1)}); !errors.Is(err, ErrAccountNotActive) {
		t.Fatalf("got %v", err)
	}
	if _, err := w.PlaceHold(money.FromKES(1)); !errors.Is(err, ErrAccountNotActive) {
		t.Fatalf("got %v", err)
	}
	w.Held = 10
	if _, err := w.ReleaseHold(money.FromKES(10)); err != nil {
		t.Fatalf("release must work on frozen accounts: %v", err)
	}
}

func TestApplyEntriesRejectsBadInput(t *testing.T) {
	w := wallet(t, "a", 100)
	cases := map[string][]Entry{
		"empty":         nil,
		"wrong account": {entry("b", Credit, 1)},
		"zero":          {entry("a", Credit, 0)},
		"negative":      {entry("a", Credit, -5)},
		"bad side":      {{AccountID: "a", Side: "x", Amount: money.FromKES(1)}},
	}
	for name, es := range cases {
		if _, _, err := w.ApplyEntries(es); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, _, err := w.ApplyEntries([]Entry{{AccountID: "a", Side: Credit, Amount: money.Money{Minor: 1, Currency: "USD"}}}); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("want currency mismatch, got %v", err)
	}
}

func TestOverflowOnApply(t *testing.T) {
	w := wallet(t, "a", math.MaxInt64-1)
	if _, _, err := w.ApplyEntries([]Entry{entry("a", Credit, 5)}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want overflow, got %v", err)
	}
}

func TestReleaseHoldBounds(t *testing.T) {
	w := wallet(t, "a", 100)
	h, _ := w.PlaceHold(money.FromKES(60))
	if _, err := h.ReleaseHold(money.FromKES(61)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("over-release must fail: %v", err)
	}
	r, err := h.ReleaseHold(money.FromKES(60))
	if err != nil || r.Held != 0 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestSortedUniqueIDs(t *testing.T) {
	got := SortedUniqueIDs([]string{"c", "a", "b", "a", "c"})
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("got %v", got)
	}
	if len(SortedUniqueIDs(nil)) != 0 {
		t.Fatal("nil should give empty")
	}
}
