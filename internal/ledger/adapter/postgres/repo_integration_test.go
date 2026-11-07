//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/codercollo/nanga/internal/ledger/adapter/postgres"
	"github.com/codercollo/nanga/internal/ledger/app"
	"github.com/codercollo/nanga/internal/ledger/app/apptest"
	"github.com/codercollo/nanga/internal/platform/id"
	m "github.com/codercollo/nanga/internal/platform/money"
)

// The same invariant + concurrency suite that runs against memory, now
// against real PostgreSQL with real row locks.
func TestConformance(t *testing.T) {
	apptest.Run(t, func(t *testing.T) app.Repository { return postgres.New(newPool(t)) })
}

func money(n int64) m.Money { return m.FromKES(n) }

func TestDatabaseRejectsMutationOfHistory(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	svc := app.NewService(postgres.New(pool), id.Generator{}, nil)
	w1, _ := svc.CreateWallet(ctx, "w1", "KES")
	clearing, _ := svc.EnsureSystemAccount(ctx, app.SystemAccountSpec{Code: "mpesa_clearing", Currency: "KES", Normal: "debit"})
	if _, err := svc.Post(ctx, app.PostCmd{IdempotencyKey: "k1", Kind: "deposit", Entries: []app.EntryInput{
		{AccountID: clearing.ID, Side: "debit", Amount: money(100)},
		{AccountID: w1.ID, Side: "credit", Amount: money(100)},
	}}); err != nil {
		t.Fatal(err)
	}

	for _, stmt := range []string{
		`UPDATE ledger.entries SET amount = 1`,
		`DELETE FROM ledger.entries`,
		`UPDATE ledger.journals SET kind = 'x'`,
		`DELETE FROM ledger.journals`,
		`TRUNCATE ledger.entries`,
		`TRUNCATE ledger.journals`,
		`DELETE FROM ledger.accounts`,
		`UPDATE ledger.accounts SET currency = 'USD'`,
		`UPDATE ledger.accounts SET normal_side = 'debit' WHERE kind = 'wallet'`,
	} {
		if _, err := pool.Exec(ctx, stmt); err == nil {
			t.Errorf("%q should have been rejected", stmt)
		}
	}
}

func TestDatabaseRejectsUnbalancedJournal(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	svc := app.NewService(postgres.New(pool), id.Generator{}, nil)
	a, _ := svc.CreateWallet(ctx, "a", "KES")
	b, _ := svc.CreateWallet(ctx, "b", "KES")

	// Bypass the application entirely: the deferred constraint trigger must
	// refuse to commit a journal whose debits and credits differ.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jid := id.NewV7()
	if _, err := tx.Exec(ctx, `INSERT INTO ledger.journals (id, idempotency_key, fingerprint, kind, currency, posted_at)
		VALUES ($1, 'raw', 'f', 'deposit', 'KES', now())`, jid); err != nil {
		t.Fatal(err)
	}
	for i, row := range []struct {
		acc, side string
		amt       int64
	}{{a.ID, "debit", 100}, {b.ID, "credit", 99}} {
		if _, err := tx.Exec(ctx, `INSERT INTO ledger.entries (journal_id, line_no, account_id, side, amount, currency, balance_after)
			VALUES ($1, $2, $3, $4, $5, 'KES', 0)`, jid, i, row.acc, row.side, row.amt); err != nil {
			t.Fatal(err)
		}
	}
	err = tx.Commit(ctx)
	if err == nil || !strings.Contains(err.Error(), "unbalanced") {
		t.Fatalf("commit of unbalanced journal must fail, got %v", err)
	}
}

func TestDatabaseNonNegativeBackstop(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	svc := app.NewService(postgres.New(pool), id.Generator{}, nil)
	w, _ := svc.CreateWallet(ctx, "w", "KES")
	if _, err := pool.Exec(ctx, `UPDATE ledger.accounts SET balance = -1 WHERE id = $1`, w.ID); err == nil {
		t.Fatal("negative wallet balance must violate accounts_non_negative")
	}
	if _, err := pool.Exec(ctx, `UPDATE ledger.accounts SET held = 5 WHERE id = $1`, w.ID); err == nil {
		t.Fatal("held > balance must violate accounts_non_negative")
	}
}

func TestHoldIsImmutableOnceResolved(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	svc := app.NewService(postgres.New(pool), id.Generator{}, nil)
	w, _ := svc.CreateWallet(ctx, "w", "KES")
	clearing, _ := svc.EnsureSystemAccount(ctx, app.SystemAccountSpec{Code: "mpesa_clearing", Currency: "KES", Normal: "debit"})
	_, _ = svc.Post(ctx, app.PostCmd{IdempotencyKey: "f", Kind: "deposit", Entries: []app.EntryInput{
		{AccountID: clearing.ID, Side: "debit", Amount: money(500)}, {AccountID: w.ID, Side: "credit", Amount: money(500)}}})
	if _, err := svc.PlaceHold(ctx, app.PlaceHoldCmd{Reference: "h1", AccountID: w.ID, Amount: money(100)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReleaseHold(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE ledger.holds SET status = 'active', resolved_at = NULL WHERE reference = 'h1'`); err == nil {
		t.Fatal("resolved hold must not be reopened")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM ledger.holds`); err == nil {
		t.Fatal("holds must not be deletable")
	}
}

func TestMigrationsDownThenUp(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t) // already migrated up
	applyMigrations(ctx, t, pool, "down")
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'ledger'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("schema should be gone after down: n=%d err=%v", n, err)
	}
	applyMigrations(ctx, t, pool, "up")
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'ledger'`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("want 4 tables after re-up, got %d (%v)", n, err)
	}
}

// Spec 4.2: besides triggers, UPDATE/DELETE on history is revoked from the
// application role. The service must still work end to end as that role.
func TestAppRoleCannotRewriteHistory(t *testing.T) {
	ctx := context.Background()
	owner := newPool(t)
	var exists bool
	if err := owner.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ledger_app')`).Scan(&exists); err != nil || !exists {
		t.Skip("ledger_app role unavailable")
	}
	cfg := owner.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE ledger_app")
		return err
	}
	appPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer appPool.Close()

	svc := app.NewService(postgres.New(appPool), id.Generator{}, nil)
	chart, err := svc.BootstrapChart(ctx)
	if err != nil {
		t.Fatalf("bootstrap as ledger_app: %v", err)
	}
	w1, err := svc.CreateWallet(ctx, "w1", "KES")
	if err != nil {
		t.Fatal(err)
	}
	w2, _ := svc.CreateWallet(ctx, "w2", "KES")
	clearing := chart[app.AccountMpesaClearing].ID
	if _, err := svc.Post(ctx, app.PostCmd{IdempotencyKey: "d", Kind: "deposit", Entries: []app.EntryInput{
		{AccountID: clearing, Side: "debit", Amount: money(1_000)}, {AccountID: w1.ID, Side: "credit", Amount: money(1_000)}}}); err != nil {
		t.Fatalf("post as ledger_app: %v", err)
	}
	if _, err := svc.PlaceHold(ctx, app.PlaceHoldCmd{Reference: "h", AccountID: w1.ID, Amount: money(300)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CaptureHold(ctx, "h", app.PostCmd{IdempotencyKey: "c", Kind: "withdrawal", Entries: []app.EntryInput{
		{AccountID: w1.ID, Side: "debit", Amount: money(300)}, {AccountID: clearing, Side: "credit", Amount: money(300)}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Reverse(ctx, app.ReverseCmd{OriginalKey: "d", IdempotencyKey: "r"}); err == nil {
		t.Fatal("reversal should fail: funds were withdrawn")
	}
	if _, err := svc.Post(ctx, app.PostCmd{IdempotencyKey: "t", Kind: "transfer", Entries: []app.EntryInput{
		{AccountID: w1.ID, Side: "debit", Amount: money(100)}, {AccountID: w2.ID, Side: "credit", Amount: money(100)}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyLedger(ctx); err != nil {
		t.Fatal(err)
	}

	for _, stmt := range []string{
		`UPDATE ledger.entries SET amount = 1`,
		`DELETE FROM ledger.entries`,
		`UPDATE ledger.journals SET kind = 'x'`,
		`DELETE FROM ledger.journals`,
		`TRUNCATE ledger.entries`,
		`TRUNCATE ledger.journals`,
		`DELETE FROM ledger.accounts`,
		`DELETE FROM ledger.holds`,
	} {
		_, err := appPool.Exec(ctx, stmt)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "42501" {
			t.Errorf("%q: want permission denied (42501), got %v", stmt, err)
		}
	}
}
