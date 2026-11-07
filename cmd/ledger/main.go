// Command ledger wires the ledger module (domain + app + Postgres adapter) and
// serves health endpoints. The gRPC API is added in Phase 2 together with
// platform/grpcx and proto/ (see phases/phase-1/context.md).
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/codercollo/nanga/internal/ledger/adapter/postgres"
	"github.com/codercollo/nanga/internal/ledger/app"
	"github.com/codercollo/nanga/internal/platform/config"
	"github.com/codercollo/nanga/internal/platform/id"
	"github.com/codercollo/nanga/internal/platform/obs"
	"github.com/codercollo/nanga/internal/platform/pg"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ledger:", err)
		os.Exit(1)
	}
}

func run() error {
	var l config.Loader
	base := config.LoadBase(&l, "ledger", ":8081")
	pgCfg := config.LoadPostgres(&l)
	if err := l.Err(); err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	log := obs.NewLogger(os.Stdout, base.LogLevel, base.LogFormat, base.Service, base.Env)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pg.NewPool(ctx, pg.Config{DSN: pgCfg.DSN, MaxConns: pgCfg.MaxConns})
	if err != nil {
		return err
	}
	defer pool.Close()

	svc := app.NewService(postgres.New(pool), id.Generator{}, nil)

	// The ledger owns the chart of accounts (mpesa_clearing, mpesa_float,
	// fees_revenue, suspense); creating them is idempotent.
	chart, err := svc.BootstrapChart(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap chart of accounts: %w", err)
	}
	log.Info("ledger ready", "system_accounts", len(chart))

	srv := &http.Server{
		Addr:              base.HTTPAddr,
		Handler:           obs.HealthMux(obs.Check{Name: "postgres", Fn: pool.Ping}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", base.HTTPAddr)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	}
	return nil
}
