package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/codercollo/nanga/internal/ledger/adapter/memory"
	"github.com/codercollo/nanga/internal/ledger/app"
	"github.com/codercollo/nanga/internal/ledger/app/apptest"
	"github.com/codercollo/nanga/internal/ledger/domain"
	"github.com/codercollo/nanga/internal/platform/id"
	"github.com/codercollo/nanga/internal/platform/money"
)

// The full conformance suite (invariants + concurrency) against memory.
func TestConformance(t *testing.T) {
	apptest.Run(t, func(*testing.T) app.Repository { return memory.New() })
}

// The memory repo is the oracle for lock discipline: out-of-order locking
// must fail loudly.
func TestRejectsOutOfOrderLocking(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	svc := app.NewService(repo, id.Generator{}, nil)
	a, _ := svc.CreateWallet(ctx, "a", money.KES)
	b, _ := svc.CreateWallet(ctx, "b", money.KES)
	hi, lo := a.ID, b.ID
	if hi < lo {
		hi, lo = lo, hi
	}
	err := repo.WithTx(ctx, func(ctx context.Context, tx app.Tx) error {
		if _, err := tx.LockAccounts(ctx, []string{hi}); err != nil {
			return err
		}
		_, err := tx.LockAccounts(ctx, []string{lo})
		return err
	})
	if !errors.Is(err, memory.ErrLockOrder) {
		t.Fatalf("want ErrLockOrder, got %v", err)
	}
}

func TestUpdateWithoutLockRejected(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	svc := app.NewService(repo, id.Generator{}, nil)
	a, _ := svc.CreateWallet(ctx, "a", money.KES)
	err := repo.WithTx(ctx, func(ctx context.Context, tx app.Tx) error {
		a.Version++
		return tx.UpdateAccount(ctx, a, 0)
	})
	if !errors.Is(err, memory.ErrNotLocked) {
		t.Fatalf("want ErrNotLocked, got %v", err)
	}
}

func TestRollbackOnError(t *testing.T) {
	ctx := context.Background()
	repo := memory.New()
	boom := errors.New("boom")
	err := repo.WithTx(ctx, func(ctx context.Context, tx app.Tx) error {
		w, _ := domain.NewWallet("w1", "owner", money.KES, app.SystemClock{}.Now())
		_ = tx.CreateAccount(ctx, w)
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := repo.GetAccount(ctx, "w1"); !errors.Is(err, domain.ErrAccountNotFound) {
		t.Fatalf("rolled-back account visible: %v", err)
	}
}
