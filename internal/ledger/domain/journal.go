package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/codercollo/nanga/internal/platform/money"
)

const (
	// MaxEntries bounds journal size (and therefore lock fan-out).
	MaxEntries = 100
	// MaxKeyLen bounds idempotency keys and references.
	MaxKeyLen = 128
)

var kindRe = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)

// Entry is one line of a journal. BalanceAfter is filled in by the ledger at
// posting time (account balance after this line, in the account's own
// normal-side convention).
type Entry struct {
	AccountID    string
	Side         Side
	Amount       money.Money
	BalanceAfter int64
}

// Journal is an immutable, balanced set of entries posted atomically.
type Journal struct {
	ID             string
	IdempotencyKey string
	Fingerprint    string
	Kind           string
	Reference      string
	Description    string
	Currency       money.Currency
	Entries        []Entry
	PostedAt       time.Time
	// ReversesID is the id of the journal this one corrects. Set only on
	// journals of kind KindReversal. Ledger history is never edited.
	ReversesID string
}

// KindReversal is the journal kind of a correcting journal.
const KindReversal = "reversal"

// NewJournalParams are the inputs to NewJournal.
type NewJournalParams struct {
	ID             string
	IdempotencyKey string
	Kind           string
	Reference      string
	Description    string
	Entries        []Entry
	PostedAt       time.Time
	ReversesID     string
}

// NewJournal validates and builds a journal. Invariants enforced here:
// at least two entries touching at least two distinct accounts; every amount
// positive; single currency; total debits == total credits; no overflow.
func NewJournal(p NewJournalParams) (Journal, error) {
	switch {
	case p.ID == "":
		return Journal{}, invalid("journal id required")
	case p.IdempotencyKey == "" || len(p.IdempotencyKey) > MaxKeyLen:
		return Journal{}, invalid("idempotency key required (<=%d chars)", MaxKeyLen)
	case !kindRe.MatchString(p.Kind):
		return Journal{}, invalid("kind must match %s", kindRe)
	case (p.ReversesID != "") != (p.Kind == KindReversal):
		return Journal{}, invalid("kind %q requires reverses_id exactly when kind is %q", p.Kind, KindReversal)
	case len(p.Reference) > MaxKeyLen:
		return Journal{}, invalid("reference too long")
	case len(p.Entries) < 2 || len(p.Entries) > MaxEntries:
		return Journal{}, invalid("journal needs 2..%d entries, got %d", MaxEntries, len(p.Entries))
	}
	cur := p.Entries[0].Amount.Currency
	if !cur.Valid() {
		return Journal{}, invalid("unsupported currency %q", cur)
	}
	var debits, credits int64
	accounts := map[string]struct{}{}
	for i, e := range p.Entries {
		if e.AccountID == "" || !e.Side.Valid() {
			return Journal{}, invalid("entry %d: account and valid side required", i)
		}
		if e.Amount.Currency != cur {
			return Journal{}, fmt.Errorf("%w: entry %d is %s, journal is %s", ErrCurrencyMismatch, i, e.Amount.Currency, cur)
		}
		if !e.Amount.IsPositive() {
			return Journal{}, invalid("entry %d: amount must be positive", i)
		}
		tot := &debits
		if e.Side == Credit {
			tot = &credits
		}
		if *tot > math.MaxInt64-e.Amount.Minor {
			return Journal{}, ErrOverflow
		}
		*tot += e.Amount.Minor
		accounts[e.AccountID] = struct{}{}
	}
	if len(accounts) < 2 {
		return Journal{}, invalid("journal must touch at least two accounts")
	}
	if debits != credits {
		return Journal{}, fmt.Errorf("%w: debits %d, credits %d", ErrUnbalancedJournal, debits, credits)
	}
	entries := append([]Entry(nil), p.Entries...)
	for i := range entries {
		entries[i].BalanceAfter = 0
	}
	j := Journal{ID: p.ID, IdempotencyKey: p.IdempotencyKey, Kind: p.Kind, Reference: p.Reference,
		Description: p.Description, Currency: cur, Entries: entries, PostedAt: p.PostedAt, ReversesID: p.ReversesID}
	j.Fingerprint = j.computeFingerprint()
	return j, nil
}

// computeFingerprint hashes the semantic content (not id, time, description,
// or entry order) so an idempotent retry can be told apart from key reuse.
func (j Journal) computeFingerprint() string {
	lines := make([]string, len(j.Entries))
	for i, e := range j.Entries {
		lines[i] = fmt.Sprintf("%s:%s:%d", e.AccountID, e.Side, e.Amount.Minor)
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte("v1|" + j.Kind + "|" + j.ReversesID + "|" + j.Reference + "|" + string(j.Currency) + "|" + strings.Join(lines, ",")))
	return hex.EncodeToString(h[:])
}

// Totals returns the sum of debit and credit entries.
func (j Journal) Totals() (debits, credits int64) {
	for _, e := range j.Entries {
		if e.Side == Debit {
			debits += e.Amount.Minor
		} else {
			credits += e.Amount.Minor
		}
	}
	return
}

// AccountIDs returns the distinct accounts touched, sorted ascending (the
// required lock order).
func (j Journal) AccountIDs() []string {
	ids := make([]string, len(j.Entries))
	for i, e := range j.Entries {
		ids[i] = e.AccountID
	}
	return SortedUniqueIDs(ids)
}

// NetChange returns the signed change to an account's balance caused by this
// journal, given the account's normal side. Valid journals bound each side's
// total by MaxInt64, so the difference cannot overflow.
func (j Journal) NetChange(accountID string, normal Side) int64 {
	var net int64
	for _, e := range j.Entries {
		if e.AccountID != accountID {
			continue
		}
		if e.Side == normal {
			net += e.Amount.Minor
		} else {
			net -= e.Amount.Minor
		}
	}
	return net
}

// TrialBalanceLine totals every entry ever posted in one currency. The ledger
// is sound only while Debits == Credits.
type TrialBalanceLine struct {
	Currency money.Currency
	Debits   int64
	Credits  int64
}

// Balanced reports whether debits equal credits.
func (l TrialBalanceLine) Balanced() bool { return l.Debits == l.Credits }
