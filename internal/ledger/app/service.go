package app

import (
	"context"
	"errors"
	"time"

	"github.com/codercollo/nanga/internal/ledger/domain"
)

// Service exposes the ledger use cases.
type Service struct {
	repo    Repository
	ids     IDGenerator
	clock   Clock
	timeout time.Duration
}

// DefaultOpTimeout bounds every use-case call (spec 4.3: every database call
// runs under a context timeout). A shorter caller deadline still wins.
const DefaultOpTimeout = 10 * time.Second

// Option customises a Service.
type Option func(*Service)

// WithOpTimeout overrides DefaultOpTimeout.
func WithOpTimeout(d time.Duration) Option { return func(s *Service) { s.timeout = d } }

// NewService wires a Service. clock may be nil (SystemClock is used).
func NewService(repo Repository, ids IDGenerator, clock Clock, opts ...Option) *Service {
	if clock == nil {
		clock = SystemClock{}
	}
	s := &Service{repo: repo, ids: ids, clock: clock, timeout: DefaultOpTimeout}
	for _, o := range opts {
		o(s)
	}
	return s
}

// bound derives the per-operation context.
func (s *Service) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.timeout)
}

// inTx runs fn in a transaction. When the transaction loses a uniqueness race
// (concurrent creation of the same key) it is run once more: the second run
// finds the winner's row and replays it.
func (s *Service) inTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error {
	ctx, cancel := s.bound(ctx)
	defer cancel()
	err := s.repo.WithTx(ctx, fn)
	if errors.Is(err, domain.ErrDuplicateKey) || errors.Is(err, domain.ErrAccountExists) {
		err = s.repo.WithTx(ctx, fn)
	}
	return err
}
