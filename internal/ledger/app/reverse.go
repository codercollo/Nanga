package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/codercollo/nanga/internal/ledger/domain"
)

// ReverseCmd corrects a posted journal. The ledger is append-only, so a
// correction is a new journal with every entry's side flipped and a link to
// the original (spec 4.2).
type ReverseCmd struct {
	OriginalKey    string // idempotency key of the journal to reverse
	IdempotencyKey string // key of the reversing journal
	Description    string
}

// Reverse posts the reversing journal. It fails with ErrAlreadyReversed when
// the original already has a reversal under a different key, with
// ErrInsufficientFunds when reversing would overdraw an account, and with
// ErrInvalid when the original is itself a reversal.
func (s *Service) Reverse(ctx context.Context, cmd ReverseCmd) (PostResult, error) {
	var res PostResult
	err := s.inTx(ctx, func(ctx context.Context, tx Tx) error {
		orig, err := tx.GetJournalByKey(ctx, cmd.OriginalKey)
		if err != nil {
			return err
		}
		if orig.ReversesID != "" {
			return fmt.Errorf("%w: a reversal cannot be reversed", domain.ErrInvalid)
		}
		entries := make([]domain.Entry, len(orig.Entries))
		for i, e := range orig.Entries {
			entries[i] = domain.Entry{AccountID: e.AccountID, Side: e.Side.Opposite(), Amount: e.Amount}
		}
		j, err := domain.NewJournal(domain.NewJournalParams{
			ID: s.ids.NewID(), IdempotencyKey: cmd.IdempotencyKey, Kind: domain.KindReversal,
			Reference: orig.Reference, Description: cmd.Description,
			Entries: entries, PostedAt: s.clock.Now(), ReversesID: orig.ID,
		})
		if err != nil {
			return err
		}
		check := func(ctx context.Context) error {
			prev, err := tx.GetReversalOf(ctx, orig.ID)
			if errors.Is(err, domain.ErrJournalNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("%w: %s reversed by %s", domain.ErrAlreadyReversed, cmd.OriginalKey, prev.IdempotencyKey)
		}
		r, err := postInTx(ctx, tx, j, check)
		res = r
		return err
	})
	if err != nil {
		return PostResult{}, err
	}
	return res, nil
}
