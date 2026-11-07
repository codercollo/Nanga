package domain

import (
	"errors"
	"math"
	"math/rand"
	"testing"

	"github.com/codercollo/nanga/internal/platform/money"
)

func params(es ...Entry) NewJournalParams {
	return NewJournalParams{ID: "j1", IdempotencyKey: "k1", Kind: "deposit", Reference: "pay-1", Entries: es, PostedAt: now}
}

func TestNewJournalValid(t *testing.T) {
	j, err := NewJournal(params(entry("clearing", Debit, 1000), entry("w", Credit, 1000)))
	if err != nil {
		t.Fatal(err)
	}
	d, c := j.Totals()
	if d != 1000 || c != 1000 || j.Currency != money.KES || j.Fingerprint == "" {
		t.Fatalf("%+v %d %d", j, d, c)
	}
}

func TestNewJournalInvariants(t *testing.T) {
	cases := []struct {
		name string
		p    NewJournalParams
		want error
	}{
		{"unbalanced", params(entry("a", Debit, 100), entry("b", Credit, 99)), ErrUnbalancedJournal},
		{"single entry", params(entry("a", Debit, 100)), ErrInvalid},
		{"one account only", params(entry("a", Debit, 100), entry("a", Credit, 100)), ErrInvalid},
		{"zero amount", params(entry("a", Debit, 0), entry("b", Credit, 0)), ErrInvalid},
		{"negative amount", params(entry("a", Debit, -5), entry("b", Credit, -5)), ErrInvalid},
		{"mixed currency", params(entry("a", Debit, 5), Entry{AccountID: "b", Side: Credit, Amount: money.Money{Minor: 5, Currency: "USD"}}), ErrCurrencyMismatch},
		{"bad side", params(entry("a", Debit, 5), Entry{AccountID: "b", Side: "x", Amount: money.FromKES(5)}), ErrInvalid},
		{"empty account", params(entry("a", Debit, 5), entry("", Credit, 5)), ErrInvalid},
	}
	for _, tc := range cases {
		if _, err := NewJournal(tc.p); !errors.Is(err, tc.want) {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}
	// unbalanced is also an ErrInvalid
	if _, err := NewJournal(params(entry("a", Debit, 100), entry("b", Credit, 99))); !errors.Is(err, ErrInvalid) {
		t.Error("unbalanced should wrap ErrInvalid")
	}
}

func TestNewJournalFieldValidation(t *testing.T) {
	good := params(entry("a", Debit, 5), entry("b", Credit, 5))
	mut := map[string]func(*NewJournalParams){
		"no id":      func(p *NewJournalParams) { p.ID = "" },
		"no key":     func(p *NewJournalParams) { p.IdempotencyKey = "" },
		"long key":   func(p *NewJournalParams) { p.IdempotencyKey = string(make([]byte, MaxKeyLen+1)) },
		"bad kind":   func(p *NewJournalParams) { p.Kind = "Bad Kind" },
		"empty kind": func(p *NewJournalParams) { p.Kind = "" },
	}
	for name, f := range mut {
		p := good
		f(&p)
		if _, err := NewJournal(p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestNewJournalTooManyEntries(t *testing.T) {
	var es []Entry
	for i := 0; i < MaxEntries/2+1; i++ {
		es = append(es, entry("a", Debit, 1), entry("b", Credit, 1))
	}
	if _, err := NewJournal(params(es...)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestNewJournalOverflow(t *testing.T) {
	_, err := NewJournal(params(entry("a", Debit, math.MaxInt64), entry("c", Debit, 1), entry("b", Credit, math.MaxInt64), entry("d", Credit, 1)))
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("want overflow, got %v", err)
	}
}

func TestFingerprintStability(t *testing.T) {
	a, _ := NewJournal(params(entry("a", Debit, 5), entry("b", Credit, 5)))
	// different order, id, time, description: same fingerprint
	p := params(entry("b", Credit, 5), entry("a", Debit, 5))
	p.ID, p.Description = "other", "memo"
	b, _ := NewJournal(p)
	if a.Fingerprint != b.Fingerprint {
		t.Fatal("fingerprint should ignore order/id/description")
	}
	// different amount: different fingerprint
	c, _ := NewJournal(params(entry("a", Debit, 6), entry("b", Credit, 6)))
	if a.Fingerprint == c.Fingerprint {
		t.Fatal("fingerprint should change with amount")
	}
	// different reference
	p = params(entry("a", Debit, 5), entry("b", Credit, 5))
	p.Reference = "pay-2"
	d, _ := NewJournal(p)
	if a.Fingerprint == d.Fingerprint {
		t.Fatal("fingerprint should change with reference")
	}
}

func TestJournalDoesNotAliasInput(t *testing.T) {
	es := []Entry{entry("a", Debit, 5), entry("b", Credit, 5)}
	j, _ := NewJournal(params(es...))
	j.Entries[0].BalanceAfter = 99
	if es[0].BalanceAfter != 0 {
		t.Fatal("journal aliases caller slice")
	}
}

func TestAccountIDsAndNetChange(t *testing.T) {
	j, _ := NewJournal(params(entry("z", Debit, 30), entry("a", Credit, 20), entry("m", Credit, 10)))
	ids := j.AccountIDs()
	if len(ids) != 3 || ids[0] != "a" || ids[1] != "m" || ids[2] != "z" {
		t.Fatalf("ids %v", ids)
	}
	if n := j.NetChange("a", Credit); n != 20 {
		t.Fatalf("net a credit-normal = %d", n)
	}
	if n := j.NetChange("z", Credit); n != -30 {
		t.Fatalf("net z credit-normal = %d", n)
	}
	if n := j.NetChange("z", Debit); n != 30 {
		t.Fatalf("net z debit-normal = %d", n)
	}
}

func TestTrialBalanceLine(t *testing.T) {
	if !(TrialBalanceLine{Debits: 5, Credits: 5}).Balanced() || (TrialBalanceLine{Debits: 5, Credits: 6}).Balanced() {
		t.Fatal("Balanced wrong")
	}
}

func TestReversalKindRequiresLink(t *testing.T) {
	es := []Entry{entry("a", Debit, 5), entry("b", Credit, 5)}
	p := params(es...)
	p.Kind = KindReversal
	if _, err := NewJournal(p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reversal without reverses_id: %v", err)
	}
	p = params(es...)
	p.ReversesID = "orig"
	if _, err := NewJournal(p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reverses_id on non-reversal: %v", err)
	}
	p.Kind = KindReversal
	j, err := NewJournal(p)
	if err != nil || j.ReversesID != "orig" {
		t.Fatalf("%+v %v", j, err)
	}
	q := p
	q.ReversesID = "other"
	k, _ := NewJournal(q)
	if j.Fingerprint == k.Fingerprint {
		t.Fatal("fingerprint must cover reverses_id")
	}
}

// Property: for any random set of entries, NewJournal accepts exactly the
// sets that are balanced over >=2 accounts with positive amounts, and every
// accepted journal has debits == credits and moves non-negative wallets
// without ever leaving them negative.
func TestPropertyJournalsBalance(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	accts := []string{"a", "b", "c", "d"}
	accepted, rejected := 0, 0
	for i := 0; i < 5000; i++ {
		n := 1 + rng.Intn(6)
		es := make([]Entry, n)
		var d, c int64
		seen := map[string]bool{}
		for k := range es {
			side := Debit
			if rng.Intn(2) == 0 {
				side = Credit
			}
			amt := int64(rng.Intn(50)) // may be 0 -> invalid
			acc := accts[rng.Intn(len(accts))]
			seen[acc] = true
			es[k] = entry(acc, side, amt)
			if amt > 0 {
				if side == Debit {
					d += amt
				} else {
					c += amt
				}
			}
		}
		allPositive := true
		for _, e := range es {
			allPositive = allPositive && e.Amount.Minor > 0
		}
		wantOK := n >= 2 && allPositive && len(seen) >= 2 && d == c
		j, err := NewJournal(params(es...))
		if (err == nil) != wantOK {
			t.Fatalf("entries %+v: wantOK=%v err=%v", es, wantOK, err)
		}
		if err != nil {
			rejected++
			continue
		}
		accepted++
		if jd, jc := j.Totals(); jd != jc {
			t.Fatalf("accepted unbalanced journal %d/%d", jd, jc)
		}
		// apply to wallets holding 40 each; invariant must hold on success
		for _, id := range j.AccountIDs() {
			w := wallet(t, id, 40)
			var mine []Entry
			for _, e := range j.Entries {
				if e.AccountID == id {
					mine = append(mine, e)
				}
			}
			if up, _, err := w.ApplyEntries(mine); err == nil && up.Available() < 0 {
				t.Fatalf("wallet went negative: %+v", up)
			} else if err != nil && !errors.Is(err, ErrInsufficientFunds) {
				t.Fatalf("unexpected error %v", err)
			}
		}
	}
	if accepted == 0 || rejected == 0 {
		t.Fatalf("generator degenerate: accepted=%d rejected=%d", accepted, rejected)
	}
}
