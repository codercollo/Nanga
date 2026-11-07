//go:build integration

package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// adminDSN points at a server where the test user may CREATE DATABASE.
func adminDSN() string {
	if d := os.Getenv("NANGA_TEST_PG_ADMIN_DSN"); d != "" {
		return d
	}
	return "postgres://nanga:nanga@localhost:5432/nanga?sslmode=disable"
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
}

// migrationFiles returns db/migrations/ledger/*.<direction>.sql in version order.
func migrationFiles(t testing.TB, direction string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(), "db", "migrations", "ledger", "*."+direction+".sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no %s migrations found: %v", direction, err)
	}
	sort.Strings(files)
	if direction == "down" {
		for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
			files[i], files[j] = files[j], files[i]
		}
	}
	return files
}

func applyMigrations(ctx context.Context, t testing.TB, pool *pgxpool.Pool, direction string) {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for _, f := range migrationFiles(t, direction) {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// PgConn().Exec uses the simple protocol, which allows multi-statement files.
		if _, err := conn.Conn().PgConn().Exec(ctx, string(sql)).ReadAll(); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(f), err)
		}
	}
}

// newPool creates a fresh database with all ledger migrations applied and
// drops it when the test ends. Skips when no Postgres is reachable.
func newPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminDSN())
	if err != nil {
		// CI and `make test-int` set NANGA_REQUIRE_PG so a missing database
		// fails loudly instead of silently skipping every test.
		if os.Getenv("NANGA_REQUIRE_PG") != "" {
			t.Fatalf("postgres required but unavailable: %v", err)
		}
		t.Skipf("postgres unavailable (%v); start it with `make up-infra`", err)
	}
	defer admin.Close(ctx)

	// Cluster-level role used by the privilege migration (000003) and its test.
	if _, err := admin.Exec(ctx, `DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ledger_app') THEN
			CREATE ROLE ledger_app NOLOGIN;
		END IF; END $$`); err != nil {
		t.Logf("could not create ledger_app role (privilege test will skip): %v", err)
	}

	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "nanga_test_" + hex.EncodeToString(b[:])
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(strings.Replace(adminDSN(), "/nanga?", "/"+name+"?", 1))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		a, err := pgx.Connect(c, adminDSN())
		if err != nil {
			return
		}
		defer a.Close(c)
		_, _ = a.Exec(c, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	applyMigrations(ctx, t, pool, "up")
	return pool
}
