package apptest

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/codercollo/nanga/internal/ledger/app"
	"github.com/codercollo/nanga/internal/ledger/domain"
	"github.com/codercollo/nanga/internal/platform/id"
)

// runN runs fn(i) for i in [0,n) concurrently, released together, and returns
// the results in index order.
func runN[T any](n int, fn func(i int) T) []T {
	out := make([]T, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			out[i] = fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
	return out
}

// 100 goroutines each try to move 10 out of a wallet holding 500: exactly 50
// may succeed and the balance must land on exactly zero, never below.
func testConcurrentDebits(e *env) {
	src, dst := e.wallet("src"), e.wallet("dst")
	e.fund(src.ID, 500)
	errs := runN(100, func(i int) error {
		_, err := e.svc.Post(e.ctx, e.transferCmd(key("d"), src.ID, dst.ID, 10))
		return err
	})
	ok, insufficient := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrInsufficientFunds):
			insufficient++
		default:
			e.t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 50 || insufficient != 50 {
		e.t.Fatalf("ok=%d insufficient=%d, want 50/50", ok, insufficient)
	}
	if e.bal(src.ID).Balance.Minor != 0 || e.bal(dst.ID).Balance.Minor != 500 {
		e.t.Fatalf("src=%d dst=%d", e.bal(src.ID).Balance.Minor, e.bal(dst.ID).Balance.Minor)
	}
	e.mustVerify()
}

// A->B and B->A transfers interleaved across many goroutines would deadlock
// without a global lock order. All must finish before the test deadline.
func testOpposingTransfers(e *env) {
	a, b := e.wallet("a"), e.wallet("b")
	e.fund(a.ID, 100_000)
	e.fund(b.ID, 100_000)
	ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
	defer cancel()
	errs := runN(200, func(i int) error {
		from, to := a.ID, b.ID
		if i%2 == 1 {
			from, to = b.ID, a.ID
		}
		_, err := e.svc.Post(ctx, e.transferCmd(key("x"), from, to, int64(1+i%7)))
		return err
	})
	for _, err := range errs {
		if err != nil {
			e.t.Fatalf("transfer failed (deadlock or timeout?): %v", err)
		}
	}
	if total := e.bal(a.ID).Balance.Minor + e.bal(b.ID).Balance.Minor; total != 200_000 {
		e.t.Fatalf("money not conserved: %d", total)
	}
	e.mustVerify()
}

// 30 goroutines race the same idempotency key: one writes, the rest replay.
func testSameKeyConcurrent(e *env) {
	src, dst := e.wallet("src"), e.wallet("dst")
	e.fund(src.ID, 1_000)
	k := key("same")
	type out struct {
		res app.PostResult
		err error
	}
	results := runN(30, func(int) out {
		r, err := e.svc.Post(e.ctx, e.transferCmd(k, src.ID, dst.ID, 100))
		return out{r, err}
	})
	fresh := 0
	for _, r := range results {
		if r.err != nil {
			e.t.Fatalf("unexpected error: %v", r.err)
		}
		if !r.res.Replayed {
			fresh++
		}
		if r.res.Journal.ID != results[0].res.Journal.ID {
			e.t.Fatal("goroutines saw different journals for one key")
		}
	}
	if fresh != 1 {
		e.t.Fatalf("%d goroutines wrote the journal, want exactly 1", fresh)
	}
	if e.bal(src.ID).Balance.Minor != 900 || e.bal(dst.ID).Balance.Minor != 100 {
		e.t.Fatal("money moved more than once")
	}
	e.mustVerify()
}

// 50 holds of 100 against 1000: exactly 10 fit.
func testConcurrentHolds(e *env) {
	w := e.wallet("w")
	e.fund(w.ID, 1_000)
	errs := runN(50, func(int) error {
		_, err := e.svc.PlaceHold(e.ctx, app.PlaceHoldCmd{Reference: key("h"), AccountID: w.ID, Amount: kes(100)})
		return err
	})
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrInsufficientFunds):
		default:
			e.t.Fatalf("unexpected error: %v", err)
		}
	}
	b := e.bal(w.ID)
	if ok != 10 || b.Held.Minor != 1_000 || b.Available.Minor != 0 {
		e.t.Fatalf("ok=%d %+v", ok, b)
	}
	e.mustVerify()
}

// Random transfers (some impossible) between 8 wallets from 16 workers.
// Money is conserved, nothing goes negative, balances equal their entries.
func testRandomTransfers(e *env) {
	const wallets, workers, perWorker, start = 8, 16, 60, 10_000
	ws := make([]string, wallets)
	for i := range ws {
		ws[i] = e.wallet("r").ID
		e.fund(ws[i], start)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for n := 0; n < perWorker; n++ {
				from, to := rng.Intn(wallets), rng.Intn(wallets)
				if from == to {
					continue
				}
				_, err := e.svc.Post(e.ctx, e.transferCmd(key("rt"), ws[from], ws[to], int64(1+rng.Intn(4_000))))
				if err != nil && !errors.Is(err, domain.ErrInsufficientFunds) {
					e.t.Errorf("unexpected error: %v", err)
					return
				}
			}
		}(int64(w) + 1)
	}
	wg.Wait()
	var total int64
	for _, w := range ws {
		b := e.bal(w).Balance.Minor
		if b < 0 {
			e.t.Fatalf("negative balance %d", b)
		}
		total += b
	}
	if total != wallets*start {
		e.t.Fatalf("money not conserved: %d != %d", total, wallets*start)
	}
	e.mustVerify()
}

// For each hold, a capture and a release race: exactly one wins, and the
// account ends in a state consistent with the winner.
func testCaptureVsRelease(e *env) {
	const holds = 10
	w := e.wallet("w")
	e.fund(w.ID, holds*100)
	refs := make([]string, holds)
	for i := range refs {
		refs[i] = key("h")
		if _, err := e.svc.PlaceHold(e.ctx, app.PlaceHoldCmd{Reference: refs[i], AccountID: w.ID, Amount: kes(100)}); err != nil {
			e.t.Fatal(err)
		}
	}
	type outcome struct{ captured, released bool }
	res := runN(holds*2, func(i int) outcome {
		ref := refs[i/2]
		if i%2 == 0 {
			_, err := e.svc.CaptureHold(e.ctx, ref, e.withdrawCmd(key("cap"), w.ID, 100))
			if err != nil && !errors.Is(err, domain.ErrHoldNotActive) {
				e.t.Errorf("capture: %v", err)
			}
			return outcome{captured: err == nil}
		}
		_, err := e.svc.ReleaseHold(e.ctx, ref)
		if err != nil && !errors.Is(err, domain.ErrHoldNotActive) {
			e.t.Errorf("release: %v", err)
		}
		return outcome{released: err == nil}
	})
	captured := 0
	for h := 0; h < holds; h++ {
		c, r := res[h*2].captured, res[h*2+1].released
		if c == r {
			e.t.Fatalf("hold %d: captured=%v released=%v, exactly one must win", h, c, r)
		}
		if c {
			captured++
		}
	}
	b := e.bal(w.ID)
	if b.Held.Minor != 0 || b.Balance.Minor != int64(holds*100-captured*100) {
		e.t.Fatalf("captured=%d state %+v", captured, b)
	}
	e.mustVerify()
}

var _ = id.NewV7
