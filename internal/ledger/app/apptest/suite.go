// Package apptest is a repository conformance suite for the ledger. The same
// invariant and concurrency tests run against every Repository
// implementation (memory in unit tests, Postgres in integration tests), so
// the two cannot drift apart.
package apptest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codercollo/nanga/internal/ledger/app"
	"github.com/codercollo/nanga/internal/ledger/domain"
	"github.com/codercollo/nanga/internal/platform/id"
	"github.com/codercollo/nanga/internal/platform/money"
)

// NewRepo returns a fresh, empty repository for one test.
type NewRepo func(t *testing.T) app.Repository

type env struct {
	t        *testing.T
	ctx      context.Context
	svc      *app.Service
	repo     app.Repository
	clearing domain.Account // debit-normal asset account, must stay >= 0
}

func newEnv(t *testing.T, newRepo NewRepo) *env {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	repo := newRepo(t)
	svc := app.NewService(repo, id.Generator{}, nil)
	chart, err := svc.BootstrapChart(ctx)
	if err != nil {
		t.Fatalf("bootstrap chart: %v", err)
	}
	return &env{t: t, ctx: ctx, svc: svc, repo: repo, clearing: chart[app.AccountMpesaClearing]}
}

func (e *env) wallet(owner string) domain.Account {
	e.t.Helper()
	w, err := e.svc.CreateWallet(e.ctx, owner+"-"+id.NewV7(), money.KES)
	if err != nil {
		e.t.Fatalf("create wallet: %v", err)
	}
	return w
}

func key(prefix string) string { return prefix + "-" + id.NewV7() }

func kes(n int64) money.Money { return money.FromKES(n) }

func (e *env) transferCmd(k string, from, to string, amount int64) app.PostCmd {
	return app.PostCmd{IdempotencyKey: k, Kind: "transfer", Reference: k, Entries: []app.EntryInput{
		{AccountID: from, Side: domain.Debit, Amount: kes(amount)},
		{AccountID: to, Side: domain.Credit, Amount: kes(amount)},
	}}
}

func (e *env) depositCmd(k, wallet string, amount int64) app.PostCmd {
	return app.PostCmd{IdempotencyKey: k, Kind: "deposit", Reference: k, Entries: []app.EntryInput{
		{AccountID: e.clearing.ID, Side: domain.Debit, Amount: kes(amount)},
		{AccountID: wallet, Side: domain.Credit, Amount: kes(amount)},
	}}
}

func (e *env) withdrawCmd(k, wallet string, amount int64) app.PostCmd {
	return app.PostCmd{IdempotencyKey: k, Kind: "withdrawal", Reference: k, Entries: []app.EntryInput{
		{AccountID: wallet, Side: domain.Debit, Amount: kes(amount)},
		{AccountID: e.clearing.ID, Side: domain.Credit, Amount: kes(amount)},
	}}
}

func (e *env) fund(wallet string, amount int64) {
	e.t.Helper()
	if _, err := e.svc.Post(e.ctx, e.depositCmd(key("fund"), wallet, amount)); err != nil {
		e.t.Fatalf("fund: %v", err)
	}
}

func (e *env) bal(accountID string) app.BalanceView {
	e.t.Helper()
	b, err := e.svc.GetBalance(e.ctx, accountID)
	if err != nil {
		e.t.Fatalf("balance: %v", err)
	}
	return b
}

func (e *env) wantErr(err, want error) {
	e.t.Helper()
	if !errors.Is(err, want) {
		e.t.Fatalf("want %v, got %v", want, err)
	}
}

func (e *env) mustVerify() {
	e.t.Helper()
	if _, err := e.svc.VerifyLedger(e.ctx); err != nil {
		e.t.Fatalf("ledger verification failed: %v", err)
	}
}

// Run executes the full suite against repositories produced by newRepo.
func Run(t *testing.T, newRepo NewRepo) {
	t.Run("Accounts", func(t *testing.T) { testAccounts(newEnv(t, newRepo)) })
	t.Run("PostingFlows", func(t *testing.T) { testPostingFlows(newEnv(t, newRepo)) })
	t.Run("InsufficientFundsIsAtomic", func(t *testing.T) { testInsufficientAtomic(newEnv(t, newRepo)) })
	t.Run("Idempotency", func(t *testing.T) { testIdempotency(newEnv(t, newRepo)) })
	t.Run("Validation", func(t *testing.T) { testValidation(newEnv(t, newRepo)) })
	t.Run("HoldsReserveFunds", func(t *testing.T) { testHoldsReserve(newEnv(t, newRepo)) })
	t.Run("HoldRelease", func(t *testing.T) { testHoldRelease(newEnv(t, newRepo)) })
	t.Run("HoldCapture", func(t *testing.T) { testHoldCapture(newEnv(t, newRepo)) })
	t.Run("ChartOfAccounts", func(t *testing.T) { testChart(newEnv(t, newRepo)) })
	t.Run("Reverse", func(t *testing.T) { testReverse(newEnv(t, newRepo)) })
	t.Run("ConcurrentReversalsOnlyOneWins", func(t *testing.T) { testConcurrentReversals(newEnv(t, newRepo)) })
	t.Run("StatementPagination", func(t *testing.T) { testStatement(newEnv(t, newRepo)) })
	t.Run("RespectsContextDeadline", func(t *testing.T) { testDeadline(newEnv(t, newRepo)) })
	t.Run("ConcurrentDebitsNeverOverdraw", func(t *testing.T) { testConcurrentDebits(newEnv(t, newRepo)) })
	t.Run("OpposingTransfersNoDeadlock", func(t *testing.T) { testOpposingTransfers(newEnv(t, newRepo)) })
	t.Run("SameKeyConcurrentPostsOnce", func(t *testing.T) { testSameKeyConcurrent(newEnv(t, newRepo)) })
	t.Run("ConcurrentHoldsCannotExceedBalance", func(t *testing.T) { testConcurrentHolds(newEnv(t, newRepo)) })
	t.Run("RandomTransfersConserveMoney", func(t *testing.T) { testRandomTransfers(newEnv(t, newRepo)) })
	t.Run("ConcurrentCaptureVersusRelease", func(t *testing.T) { testCaptureVsRelease(newEnv(t, newRepo)) })
}

func testAccounts(e *env) {
	owner := "owner-" + id.NewV7()
	a, err := e.svc.CreateWallet(e.ctx, owner, money.KES)
	if err != nil {
		e.t.Fatal(err)
	}
	b, err := e.svc.CreateWallet(e.ctx, owner, money.KES)
	if err != nil || b.ID != a.ID {
		e.t.Fatalf("CreateWallet must be idempotent: %v %v", b.ID, err)
	}
	if a.Normal != domain.Credit || a.AllowNegative {
		e.t.Fatalf("wallet shape: %+v", a)
	}
	got, err := e.svc.GetWalletBalance(e.ctx, owner, money.KES)
	if err != nil || got.AccountID != a.ID || got.Balance.Minor != 0 {
		e.t.Fatalf("wallet balance: %+v %v", got, err)
	}
	if _, err := e.svc.GetBalance(e.ctx, id.NewV7()); !errors.Is(err, domain.ErrAccountNotFound) {
		e.t.Fatalf("unknown account: %v", err)
	}
	if _, err := e.svc.CreateWallet(e.ctx, "", money.KES); !errors.Is(err, domain.ErrInvalid) {
		e.t.Fatalf("empty owner: %v", err)
	}

	again, err := e.svc.EnsureSystemAccount(e.ctx, app.SystemAccountSpec{Code: "mpesa_clearing", Currency: money.KES, Normal: domain.Debit})
	if err != nil || again.ID != e.clearing.ID {
		e.t.Fatalf("EnsureSystemAccount must be idempotent: %v %v", again.ID, err)
	}
	_, err = e.svc.EnsureSystemAccount(e.ctx, app.SystemAccountSpec{Code: "mpesa_clearing", Currency: money.KES, Normal: domain.Credit})
	e.wantErr(err, domain.ErrAccountExists)

	// Concurrent creation of the same wallet yields exactly one account.
	racer := "racer-" + id.NewV7()
	ids := runN(20, func(int) string {
		w, err := e.svc.CreateWallet(e.ctx, racer, money.KES)
		if err != nil {
			return "ERR:" + err.Error()
		}
		return w.ID
	})
	for _, got := range ids {
		if got != ids[0] {
			e.t.Fatalf("concurrent CreateWallet produced different ids: %v", ids)
		}
	}
}

func testPostingFlows(e *env) {
	w1, w2 := e.wallet("w1"), e.wallet("w2")
	dep, err := e.svc.Post(e.ctx, e.depositCmd(key("dep"), w1.ID, 10_000))
	if err != nil || dep.Replayed {
		e.t.Fatalf("deposit: %+v %v", dep, err)
	}
	for _, en := range dep.Journal.Entries {
		if en.AccountID == w1.ID && en.BalanceAfter != 10_000 {
			e.t.Fatalf("balance_after = %d", en.BalanceAfter)
		}
	}
	if _, err := e.svc.Post(e.ctx, e.transferCmd(key("tr"), w1.ID, w2.ID, 3_000)); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.Post(e.ctx, e.withdrawCmd(key("wd"), w2.ID, 1_000)); err != nil {
		e.t.Fatal(err)
	}
	if b := e.bal(w1.ID).Balance.Minor; b != 7_000 {
		e.t.Fatalf("w1 = %d", b)
	}
	if b := e.bal(w2.ID).Balance.Minor; b != 2_000 {
		e.t.Fatalf("w2 = %d", b)
	}
	if b := e.bal(e.clearing.ID).Balance.Minor; b < 9_000 { // other funds may exist; at least our net
		e.t.Fatalf("clearing = %d", b)
	}
	lines, err := e.svc.TrialBalance(e.ctx)
	if err != nil || len(lines) != 1 || !lines[0].Balanced() || lines[0].Debits == 0 {
		e.t.Fatalf("trial balance: %+v %v", lines, err)
	}
	n, err := e.svc.VerifyLedger(e.ctx)
	if err != nil || n < 3 {
		e.t.Fatalf("verify: %d %v", n, err)
	}
}

func testInsufficientAtomic(e *env) {
	w1, w2 := e.wallet("w1"), e.wallet("w2")
	e.fund(w1.ID, 1_000)
	k := key("big")
	_, err := e.svc.Post(e.ctx, e.transferCmd(k, w1.ID, w2.ID, 1_001))
	e.wantErr(err, domain.ErrInsufficientFunds)
	if e.bal(w1.ID).Balance.Minor != 1_000 || e.bal(w2.ID).Balance.Minor != 0 {
		e.t.Fatal("failed post changed balances")
	}
	if _, err := e.repo.GetJournalByKey(e.ctx, k); !errors.Is(err, domain.ErrJournalNotFound) {
		e.t.Fatalf("failed post left a journal: %v", err)
	}
	// The key is still usable once funds exist: failure consumed nothing.
	e.fund(w1.ID, 1)
	if _, err := e.svc.Post(e.ctx, e.transferCmd(k, w1.ID, w2.ID, 1_001)); err != nil {
		e.t.Fatalf("retry after top-up: %v", err)
	}
	e.mustVerify()
}

func testIdempotency(e *env) {
	w1, w2 := e.wallet("w1"), e.wallet("w2")
	e.fund(w1.ID, 5_000)
	k := key("idem")
	first, err := e.svc.Post(e.ctx, e.transferCmd(k, w1.ID, w2.ID, 1_000))
	if err != nil || first.Replayed {
		e.t.Fatalf("first: %+v %v", first, err)
	}
	second, err := e.svc.Post(e.ctx, e.transferCmd(k, w1.ID, w2.ID, 1_000))
	if err != nil || !second.Replayed || second.Journal.ID != first.Journal.ID {
		e.t.Fatalf("replay: %+v %v", second, err)
	}
	if len(second.Journal.Entries) != 2 {
		e.t.Fatalf("replayed journal lost entries: %+v", second.Journal)
	}
	if e.bal(w1.ID).Balance.Minor != 4_000 || e.bal(w2.ID).Balance.Minor != 1_000 {
		e.t.Fatal("replay moved money twice")
	}
	_, err = e.svc.Post(e.ctx, e.transferCmd(k, w1.ID, w2.ID, 2_000))
	e.wantErr(err, domain.ErrIdempotencyConflict)
	_, err = e.svc.Post(e.ctx, e.transferCmd(k, w2.ID, w1.ID, 1_000))
	e.wantErr(err, domain.ErrIdempotencyConflict)
}

func testValidation(e *env) {
	w1 := e.wallet("w1")
	e.fund(w1.ID, 100)
	_, err := e.svc.Post(e.ctx, app.PostCmd{IdempotencyKey: key("u"), Kind: "x", Entries: []app.EntryInput{
		{AccountID: w1.ID, Side: domain.Debit, Amount: kes(10)},
		{AccountID: e.clearing.ID, Side: domain.Credit, Amount: kes(9)},
	}})
	e.wantErr(err, domain.ErrUnbalancedJournal)
	_, err = e.svc.Post(e.ctx, e.transferCmd(key("ghost"), w1.ID, id.NewV7(), 10))
	e.wantErr(err, domain.ErrAccountNotFound)
	if e.bal(w1.ID).Balance.Minor != 100 {
		e.t.Fatal("rejected posting changed balance")
	}
	_, err = e.svc.Post(e.ctx, app.PostCmd{IdempotencyKey: "", Kind: "x", Entries: e.transferCmd("k", w1.ID, e.clearing.ID, 1).Entries})
	e.wantErr(err, domain.ErrInvalid)
	_, err = e.svc.PlaceHold(e.ctx, app.PlaceHoldCmd{Reference: key("h"), AccountID: id.NewV7(), Amount: kes(1)})
	e.wantErr(err, domain.ErrAccountNotFound)
	_, err = e.svc.PlaceHold(e.ctx, app.PlaceHoldCmd{Reference: key("h"), AccountID: w1.ID, Amount: kes(0)})
	e.wantErr(err, domain.ErrInvalid)
	_, err = e.svc.ReleaseHold(e.ctx, "no-such-hold")
	e.wantErr(err, domain.ErrHoldNotFound)
	e.mustVerify()
}

func testHoldsReserve(e *env) {
	w1, w2 := e.wallet("w1"), e.wallet("w2")
	e.fund(w1.ID, 10_000)
	ref := key("hold")
	h, err := e.svc.PlaceHold(e.ctx, app.PlaceHoldCmd{Reference: ref, AccountID: w1.ID, Amount: kes(4_000)})
	if err != nil || h.Replayed || h.Hold.Status != domain.HoldActive {
		e.t.Fatalf("%+v %v", h, err)
	}
	b := e.bal(w1.ID)
	if b.Balance.Minor != 10_000 || b.Held.Minor != 4_000 || b.Available.Minor != 6_000 {
		e.t.Fatalf("after hold: %+v", b)
	}
	_, err = e.svc.Post(e.ctx, e.transferCmd(key("over"), w1.ID, w2.ID, 6_001))
	e.wantErr(err, domain.ErrInsufficientFunds)
	if _, err := e.svc.Post(e.ctx, e.transferCmd(key("ok"), w1.ID, w2.ID, 6_000)); err != nil {
		e.t.Fatalf("available funds must be spendable: %v", err)
	}
	_, err = e.svc.PlaceHold(e.ctx, app.PlaceHoldCmd{Reference: key("h2"), AccountID: w1.ID, Amount: kes(1)})
	e.wantErr(err, domain.ErrInsufficientFunds)
	again, err := e.svc.PlaceHold(e.ctx, app.PlaceHoldCmd{Reference: ref, AccountID: w1.ID, Amount: kes(4_000)})
	if err != nil || !again.Replayed || again.Hold.ID != h.Hold.ID {
		e.t.Fatalf("hold replay: %+v %v", again, err)
	}
	if e.bal(w1.ID).Held.Minor != 4_000 {
		e.t.Fatal("replayed hold reserved twice")
	}
	_, err = e.svc.PlaceHold(e.ctx, app.PlaceHoldCmd{Reference: ref, AccountID: w1.ID, Amount: kes(3_999)})
	e.wantErr(err, domain.ErrHoldConflict)
	e.mustVerify()
}

func testHoldRelease(e *env) {
	w1 := e.wallet("w1")
	e.fund(w1.ID, 1_000)
	ref := key("hold")
	if _, err := e.svc.PlaceHold(e.ctx, app.PlaceHoldCmd{Reference: ref, AccountID: w1.ID, Amount: kes(700)}); err != nil {
		e.t.Fatal(err)
	}
	h, err := e.svc.ReleaseHold(e.ctx, ref)
	if err != nil || h.Status != domain.HoldReleased {
		e.t.Fatalf("%+v %v", h, err)
	}
	if b := e.bal(w1.ID); b.Held.Minor != 0 || b.Available.Minor != 1_000 {
		e.t.Fatalf("after release: %+v", b)
	}
	if _, err := e.svc.ReleaseHold(e.ctx, ref); err != nil {
		e.t.Fatalf("double release must be a no-op: %v", err)
	}
	if b := e.bal(w1.ID); b.Held.Minor != 0 {
		e.t.Fatalf("double release changed held: %+v", b)
	}
	_, err = e.svc.CaptureHold(e.ctx, ref, e.withdrawCmd(key("cap"), w1.ID, 700))
	e.wantErr(err, domain.ErrHoldNotActive)
	e.mustVerify()
}

func testHoldCapture(e *env) {
	w1, w2 := e.wallet("w1"), e.wallet("w2")
	e.fund(w1.ID, 10_000)
	ref := key("hold")
	if _, err := e.svc.PlaceHold(e.ctx, app.PlaceHoldCmd{Reference: ref, AccountID: w1.ID, Amount: kes(4_000)}); err != nil {
		e.t.Fatal(err)
	}
	// wrong amount, wrong account: rejected, hold intact
	_, err := e.svc.CaptureHold(e.ctx, ref, e.withdrawCmd(key("c1"), w1.ID, 3_999))
	e.wantErr(err, domain.ErrHoldMismatch)
	_, err = e.svc.CaptureHold(e.ctx, ref, e.withdrawCmd(key("c2"), w2.ID, 4_000))
	e.wantErr(err, domain.ErrHoldMismatch)
	if b := e.bal(w1.ID); b.Held.Minor != 4_000 || b.Balance.Minor != 10_000 {
		e.t.Fatalf("rejected capture changed state: %+v", b)
	}

	ck := key("cap")
	res, err := e.svc.CaptureHold(e.ctx, ref, e.withdrawCmd(ck, w1.ID, 4_000))
	if err != nil || res.Replayed || res.Hold.Status != domain.HoldCaptured || res.Hold.JournalID != res.Journal.ID {
		e.t.Fatalf("capture: %+v %v", res, err)
	}
	if b := e.bal(w1.ID); b.Balance.Minor != 6_000 || b.Held.Minor != 0 || b.Available.Minor != 6_000 {
		e.t.Fatalf("after capture: %+v", b)
	}
	replay, err := e.svc.CaptureHold(e.ctx, ref, e.withdrawCmd(ck, w1.ID, 4_000))
	if err != nil || !replay.Replayed || replay.Journal.ID != res.Journal.ID {
		e.t.Fatalf("capture replay: %+v %v", replay, err)
	}
	if e.bal(w1.ID).Balance.Minor != 6_000 {
		e.t.Fatal("capture replay moved money twice")
	}
	_, err = e.svc.CaptureHold(e.ctx, ref, e.withdrawCmd(key("other"), w1.ID, 4_000))
	e.wantErr(err, domain.ErrHoldNotActive)
	_, err = e.svc.ReleaseHold(e.ctx, ref)
	e.wantErr(err, domain.ErrHoldNotActive)
	e.mustVerify()
}
