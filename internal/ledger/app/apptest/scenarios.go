package apptest

import (
	"context"
	"errors"
	"time"

	"github.com/codercollo/nanga/internal/ledger/app"
	"github.com/codercollo/nanga/internal/ledger/domain"
	"github.com/codercollo/nanga/internal/platform/id"
)

func testChart(e *env) {
	chart, err := e.svc.BootstrapChart(e.ctx)
	if err != nil || len(chart) != 4 {
		e.t.Fatalf("chart: %v %v", chart, err)
	}
	again, err := e.svc.BootstrapChart(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, code := range []string{app.AccountMpesaClearing, app.AccountMpesaFloat, app.AccountFeesRevenue, app.AccountSuspense} {
		a, ok := chart[code]
		if !ok || a.ID != again[code].ID || a.Kind != domain.KindSystem {
			e.t.Fatalf("%s missing or not idempotent", code)
		}
	}
	if !chart[app.AccountSuspense].AllowNegative || chart[app.AccountMpesaFloat].AllowNegative {
		e.t.Fatal("suspense may go negative; float may not")
	}
	if chart[app.AccountFeesRevenue].Normal != domain.Credit || chart[app.AccountMpesaFloat].Normal != domain.Debit {
		e.t.Fatal("normal sides wrong")
	}
}

func testReverse(e *env) {
	w := e.wallet("w")
	depKey := key("dep")
	dep, err := e.svc.Post(e.ctx, e.depositCmd(depKey, w.ID, 1_000))
	if err != nil {
		e.t.Fatal(err)
	}
	rk := key("rev")
	rev, err := e.svc.Reverse(e.ctx, app.ReverseCmd{OriginalKey: depKey, IdempotencyKey: rk, Description: "mistake"})
	if err != nil || rev.Replayed {
		e.t.Fatalf("reverse: %+v %v", rev, err)
	}
	if rev.Journal.ReversesID != dep.Journal.ID || rev.Journal.Kind != domain.KindReversal {
		e.t.Fatalf("link wrong: %+v", rev.Journal)
	}
	for i, en := range rev.Journal.Entries {
		if en.Side != dep.Journal.Entries[i].Side.Opposite() || en.Amount != dep.Journal.Entries[i].Amount {
			e.t.Fatalf("entry %d not the mirror image", i)
		}
	}
	if e.bal(w.ID).Balance.Minor != 0 {
		e.t.Fatal("wallet not restored")
	}
	// the original journal is untouched
	orig, err := e.repo.GetJournalByKey(e.ctx, depKey)
	if err != nil || orig.ID != dep.Journal.ID || orig.ReversesID != "" {
		e.t.Fatalf("original changed: %+v %v", orig, err)
	}
	got, err := e.repo.GetReversalOf(e.ctx, dep.Journal.ID)
	if err != nil || got.ID != rev.Journal.ID {
		e.t.Fatalf("GetReversalOf: %+v %v", got, err)
	}
	// replay with the same key is a no-op
	again, err := e.svc.Reverse(e.ctx, app.ReverseCmd{OriginalKey: depKey, IdempotencyKey: rk})
	if err != nil || !again.Replayed || again.Journal.ID != rev.Journal.ID {
		e.t.Fatalf("replay: %+v %v", again, err)
	}
	// second reversal under another key is refused
	_, err = e.svc.Reverse(e.ctx, app.ReverseCmd{OriginalKey: depKey, IdempotencyKey: key("rev2")})
	e.wantErr(err, domain.ErrAlreadyReversed)
	// a reversal cannot be reversed; unknown originals are not found
	_, err = e.svc.Reverse(e.ctx, app.ReverseCmd{OriginalKey: rk, IdempotencyKey: key("rr")})
	e.wantErr(err, domain.ErrInvalid)
	_, err = e.svc.Reverse(e.ctx, app.ReverseCmd{OriginalKey: "nope-" + id.NewV7(), IdempotencyKey: key("x")})
	e.wantErr(err, domain.ErrJournalNotFound)
	e.mustVerify()

	// Reversing a deposit whose funds were spent is refused atomically,
	// and succeeds once the funds exist again.
	w2, w3 := e.wallet("w2"), e.wallet("w3")
	dk := key("dep2")
	if _, err := e.svc.Post(e.ctx, e.depositCmd(dk, w2.ID, 500)); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.Post(e.ctx, e.transferCmd(key("spend"), w2.ID, w3.ID, 500)); err != nil {
		e.t.Fatal(err)
	}
	_, err = e.svc.Reverse(e.ctx, app.ReverseCmd{OriginalKey: dk, IdempotencyKey: key("r3")})
	e.wantErr(err, domain.ErrInsufficientFunds)
	if e.bal(w3.ID).Balance.Minor != 500 {
		e.t.Fatal("failed reversal changed balances")
	}
	if _, err := e.svc.Post(e.ctx, e.transferCmd(key("back"), w3.ID, w2.ID, 500)); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.Reverse(e.ctx, app.ReverseCmd{OriginalKey: dk, IdempotencyKey: key("r4")}); err != nil {
		e.t.Fatalf("reversal after top-up: %v", err)
	}
	e.mustVerify()
}

func testConcurrentReversals(e *env) {
	w := e.wallet("w")
	dk := key("dep")
	if _, err := e.svc.Post(e.ctx, e.depositCmd(dk, w.ID, 1_000)); err != nil {
		e.t.Fatal(err)
	}
	errs := runN(10, func(int) error {
		_, err := e.svc.Reverse(e.ctx, app.ReverseCmd{OriginalKey: dk, IdempotencyKey: key("rev")})
		return err
	})
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrAlreadyReversed):
		default:
			e.t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		e.t.Fatalf("%d reversals succeeded, want exactly 1", ok)
	}
	if e.bal(w.ID).Balance.Minor != 0 {
		e.t.Fatal("reversed more than once")
	}
	e.mustVerify()
}

func testStatement(e *env) {
	w1, w2 := e.wallet("w1"), e.wallet("w2")
	e.fund(w1.ID, 10_000)
	for i := 1; i <= 6; i++ {
		if _, err := e.svc.Post(e.ctx, e.transferCmd(key("t"), w1.ID, w2.ID, int64(i*100))); err != nil {
			e.t.Fatal(err)
		}
	}
	// w2 received 100..600 -> 7 lines? no: only 6. w1 has 1 fund + 6 debits = 7.
	var all []domain.StatementLine
	var before int64
	pages := 0
	for {
		pg, err := e.svc.Statement(e.ctx, w1.ID, before, 3)
		if err != nil {
			e.t.Fatal(err)
		}
		pages++
		all = append(all, pg.Lines...)
		if pg.NextBefore == 0 {
			break
		}
		before = pg.NextBefore
		if pages > 10 {
			e.t.Fatal("pagination does not terminate")
		}
	}
	if len(all) != 7 || pages != 3 {
		e.t.Fatalf("lines=%d pages=%d, want 7 lines in 3 pages", len(all), pages)
	}
	for i := 1; i < len(all); i++ {
		if all[i].Seq >= all[i-1].Seq {
			e.t.Fatal("statement must be strictly newest-first without duplicates")
		}
	}
	if all[0].Amount.Minor != 600 || all[0].Side != domain.Debit || all[0].BalanceAfter != 10_000-2_100 {
		e.t.Fatalf("newest line wrong: %+v", all[0])
	}
	if last := all[6]; last.Kind != "deposit" || last.Side != domain.Credit || last.BalanceAfter != 10_000 {
		e.t.Fatalf("oldest line wrong: %+v", last)
	}
	// exact-fit page: no phantom next cursor
	pg, err := e.svc.Statement(e.ctx, w2.ID, 0, 6)
	if err != nil || len(pg.Lines) != 6 || pg.NextBefore != 0 {
		e.t.Fatalf("exact page: %d lines next=%d err=%v", len(pg.Lines), pg.NextBefore, err)
	}
	// limit is bounded; empty and unknown accounts behave
	if pg, err := e.svc.Statement(e.ctx, w1.ID, 0, 100_000); err != nil || len(pg.Lines) != 7 {
		e.t.Fatalf("clamped limit: %d %v", len(pg.Lines), err)
	}
	empty := e.wallet("empty")
	if pg, err := e.svc.Statement(e.ctx, empty.ID, 0, 10); err != nil || len(pg.Lines) != 0 || pg.NextBefore != 0 {
		e.t.Fatalf("empty: %+v %v", pg, err)
	}
	_, err = e.svc.Statement(e.ctx, id.NewV7(), 0, 10)
	e.wantErr(err, domain.ErrAccountNotFound)
}

// While another transaction holds the accounts, a Post under a short timeout
// must give up with the context error instead of hanging (spec 4.3/8).
func testDeadline(e *env) {
	w1, w2 := e.wallet("w1"), e.wallet("w2")
	e.fund(w1.ID, 1_000)
	short := app.NewService(e.repo, id.Generator{}, nil, app.WithOpTimeout(300*time.Millisecond))

	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- e.repo.WithTx(e.ctx, func(ctx context.Context, tx app.Tx) error {
			if _, err := tx.LockAccounts(ctx, domain.SortedUniqueIDs([]string{w1.ID, w2.ID})); err != nil {
				close(locked)
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	start := time.Now()
	_, err := short.Post(e.ctx, e.transferCmd(key("blocked"), w1.ID, w2.ID, 100))
	elapsed := time.Since(start)
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		e.t.Fatalf("want deadline exceeded, got %v", err)
	}
	if elapsed > 5*time.Second {
		e.t.Fatalf("operation ignored its timeout: %v", elapsed)
	}
	if err := <-done; err != nil {
		e.t.Fatalf("lock holder: %v", err)
	}
	// nothing was written, and the service works again once the lock is gone
	if e.bal(w1.ID).Balance.Minor != 1_000 {
		e.t.Fatal("timed-out post changed balance")
	}
	if _, err := short.Post(e.ctx, e.transferCmd(key("after"), w1.ID, w2.ID, 100)); err != nil {
		e.t.Fatalf("post after release: %v", err)
	}
	e.mustVerify()
}
