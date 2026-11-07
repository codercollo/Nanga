package money

import (
	"errors"
	"math"
	"testing"
)

func TestNewValidatesCurrency(t *testing.T) {
	if _, err := New(1, "USD"); !errors.Is(err, ErrInvalidCurrency) {
		t.Fatalf("want ErrInvalidCurrency, got %v", err)
	}
	if m, err := New(100, KES); err != nil || m.Minor != 100 {
		t.Fatalf("unexpected %v %v", m, err)
	}
}

func TestAddSub(t *testing.T) {
	a, b := FromKES(500), FromKES(200)
	s, err := a.Add(b)
	if err != nil || s.Minor != 700 {
		t.Fatalf("add: %v %v", s, err)
	}
	d, err := a.Sub(b)
	if err != nil || d.Minor != 300 {
		t.Fatalf("sub: %v %v", d, err)
	}
	if _, err := a.Add(Money{Minor: 1, Currency: "USD"}); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("want mismatch, got %v", err)
	}
}

func TestOverflow(t *testing.T) {
	if _, err := FromKES(math.MaxInt64).Add(FromKES(1)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("add overflow: %v", err)
	}
	if _, err := FromKES(math.MinInt64).Sub(FromKES(1)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("sub overflow: %v", err)
	}
	if _, err := FromKES(math.MinInt64).Neg(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("neg overflow: %v", err)
	}
}

func TestCmp(t *testing.T) {
	if c, _ := FromKES(1).Cmp(FromKES(2)); c != -1 {
		t.Fatal("want -1")
	}
	if c, _ := FromKES(2).Cmp(FromKES(2)); c != 0 {
		t.Fatal("want 0")
	}
	if c, _ := FromKES(3).Cmp(FromKES(2)); c != 1 {
		t.Fatal("want 1")
	}
}

func TestString(t *testing.T) {
	cases := map[int64]string{
		0:             "KES 0.00",
		5:             "KES 0.05",
		123450:        "KES 1,234.50",
		100000000:     "KES 1,000,000.00",
		-500:          "-KES 5.00",
		math.MinInt64: "-KES 92,233,720,368,547,758.08",
	}
	for in, want := range cases {
		if got := FromKES(in).String(); got != want {
			t.Errorf("%d: got %q want %q", in, got, want)
		}
	}
}

func TestParseMajor(t *testing.T) {
	ok := map[string]int64{"0": 0, "1": 100, "1.5": 150, "1.05": 105, "1234.56": 123456, "-2.00": -200, "0.01": 1}
	for in, want := range ok {
		m, err := ParseMajor(in, KES)
		if err != nil || m.Minor != want {
			t.Errorf("%q: got %v %v want %d", in, m, err, want)
		}
	}
	for _, in := range []string{"", ".5", "5.", "1.234", "abc", "1,000", " 1", "+1", "1.2.3", "--1"} {
		if _, err := ParseMajor(in, KES); !errors.Is(err, ErrInvalidAmount) {
			t.Errorf("%q: want ErrInvalidAmount, got %v", in, err)
		}
	}
	if _, err := ParseMajor("92233720368547758.08", KES); !errors.Is(err, ErrOverflow) {
		t.Errorf("want overflow, got %v", err)
	}
}
