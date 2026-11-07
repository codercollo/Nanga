//go:build integration

package pg

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func dsn(t *testing.T) string {
	t.Helper()
	d := os.Getenv("NANGA_TEST_PG_ADMIN_DSN")
	if d == "" {
		d = "postgres://nanga:nanga@localhost:5432/nanga?sslmode=disable"
	}
	return d
}

func TestWithTxCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	pool, err := NewPool(ctx, Config{DSN: dsn(t), MaxConns: 4})
	if err != nil {
		if os.Getenv("NANGA_REQUIRE_PG") != "" {
			t.Fatalf("postgres required but unavailable: %v", err)
		}
		t.Skipf("postgres unavailable: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.pg_tx_probe(n int)`); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DROP TABLE public.pg_tx_probe`)
	pool.Exec(ctx, `DELETE FROM public.pg_tx_probe`)

	if err := WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO public.pg_tx_probe VALUES (1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := WithTx(ctx, pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, _ = tx.Exec(ctx, `INSERT INTO public.pg_tx_probe VALUES (2)`)
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.pg_tx_probe`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("count=%d err=%v (rollback failed?)", n, err)
	}
}
