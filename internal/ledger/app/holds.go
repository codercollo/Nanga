package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/codercollo/nanga/internal/ledger/domain"
	"github.com/codercollo/nanga/internal/platform/money"
)

// PlaceHoldCmd reserves funds. Reference is the caller's unique key
// (typically the payment id) and makes the call idempotent.
type PlaceHoldCmd struct {
	Reference string
	AccountID string
	Amount    money.Money
}

// HoldResult is a hold; Replayed is true when Reference already existed.
type HoldResult struct {
	Hold     domain.Hold
	Replayed bool
}

// PlaceHold reserves Amount from the account's available balance.
func (s *Service) PlaceHold(ctx context.Context, cmd PlaceHoldCmd) (HoldResult, error) {
	h, err := domain.NewHold(s.ids.NewID(), cmd.AccountID, cmd.Reference, cmd.Amount, s.clock.Now())
	if err != nil {
		return HoldResult{}, err
	}
	var res HoldResult
	err = s.inTx(ctx, func(ctx context.Context, tx Tx) error {
		replay := func() (bool, error) {
			existing, err := tx.GetHoldByRef(ctx, cmd.Reference)
			if errors.Is(err, domain.ErrHoldNotFound) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			if existing.AccountID != cmd.AccountID || existing.Amount != cmd.Amount {
				return false, fmt.Errorf("%w: %s", domain.ErrHoldConflict, cmd.Reference)
			}
			res = HoldResult{Hold: existing, Replayed: true}
			return true, nil
		}
		if done, err := replay(); err != nil || done {
			return err
		}
		locked, err := lockAll(ctx, tx, []string{cmd.AccountID})
		if err != nil {
			return err
		}
		if done, err := replay(); err != nil || done {
			return err
		}
		la := locked[cmd.AccountID]
		updated, err := la.acct.PlaceHold(cmd.Amount)
		if err != nil {
			return err
		}
		if err := tx.InsertHold(ctx, h); err != nil {
			return err
		}
		if err := tx.UpdateAccount(ctx, updated, la.version); err != nil {
			return err
		}
		res = HoldResult{Hold: h}
		return nil
	})
	if err != nil {
		return HoldResult{}, err
	}
	return res, nil
}

// ReleaseHold returns held funds to the available balance. Releasing an
// already-released hold is a no-op; releasing a captured hold fails.
func (s *Service) ReleaseHold(ctx context.Context, reference string) (domain.Hold, error) {
	var out domain.Hold
	err := s.inTx(ctx, func(ctx context.Context, tx Tx) error {
		h, err := tx.GetHoldByRef(ctx, reference)
		if err != nil {
			return err
		}
		locked, err := lockAll(ctx, tx, []string{h.AccountID})
		if err != nil {
			return err
		}
		h, err = tx.GetHoldByRef(ctx, reference) // re-read under lock
		if err != nil {
			return err
		}
		if h.Status == domain.HoldReleased {
			out = h
			return nil
		}
		released, err := h.Release(s.clock.Now())
		if err != nil {
			return err
		}
		la := locked[h.AccountID]
		updated, err := la.acct.ReleaseHold(h.Amount)
		if err != nil {
			return err
		}
		if err := tx.UpdateHold(ctx, released); err != nil {
			return err
		}
		if err := tx.UpdateAccount(ctx, updated, la.version); err != nil {
			return err
		}
		out = released
		return nil
	})
	return out, err
}

// CaptureResult is the outcome of CaptureHold.
type CaptureResult struct {
	Hold     domain.Hold
	Journal  domain.Journal
	Replayed bool
}

// CaptureHold settles a hold: it releases the reservation and posts Post in
// the same transaction. The journal must reduce the held account's balance by
// exactly the held amount. Repeating the call with the same journal key
// replays the original result.
func (s *Service) CaptureHold(ctx context.Context, reference string, post PostCmd) (CaptureResult, error) {
	j, err := s.buildJournal(post)
	if err != nil {
		return CaptureResult{}, err
	}
	var res CaptureResult
	err = s.inTx(ctx, func(ctx context.Context, tx Tx) error {
		h, err := tx.GetHoldByRef(ctx, reference)
		if err != nil {
			return err
		}
		locked, err := lockAll(ctx, tx, append(j.AccountIDs(), h.AccountID))
		if err != nil {
			return err
		}
		h, err = tx.GetHoldByRef(ctx, reference) // re-read under lock
		if err != nil {
			return err
		}
		switch h.Status {
		case domain.HoldCaptured:
			existing, ok, err := replayOf(ctx, tx, j)
			if err != nil {
				return err
			}
			if ok && existing.ID == h.JournalID {
				res = CaptureResult{Hold: h, Journal: existing, Replayed: true}
				return nil
			}
			return fmt.Errorf("%w: %s already captured", domain.ErrHoldNotActive, reference)
		case domain.HoldReleased:
			return fmt.Errorf("%w: %s is released", domain.ErrHoldNotActive, reference)
		}
		if _, ok, err := replayOf(ctx, tx, j); err != nil {
			return err
		} else if ok {
			return fmt.Errorf("%w: key %q already used for another journal", domain.ErrIdempotencyConflict, j.IdempotencyKey)
		}
		held := locked[h.AccountID]
		if j.Currency != h.Amount.Currency {
			return fmt.Errorf("%w: journal %s vs hold %s", domain.ErrCurrencyMismatch, j.Currency, h.Amount.Currency)
		}
		if j.NetChange(h.AccountID, held.acct.Normal) != -h.Amount.Minor {
			return fmt.Errorf("%w: journal must debit exactly %d from %s", domain.ErrHoldMismatch, h.Amount.Minor, h.AccountID)
		}
		released, err := held.acct.ReleaseHold(h.Amount)
		if err != nil {
			return err
		}
		held.acct = released // version guard still uses the loaded version
		posted, err := applyJournal(ctx, tx, j, locked)
		if err != nil {
			return err
		}
		captured, err := h.Capture(s.clock.Now(), posted.ID)
		if err != nil {
			return err
		}
		if err := tx.UpdateHold(ctx, captured); err != nil {
			return err
		}
		res = CaptureResult{Hold: captured, Journal: posted}
		return nil
	})
	if err != nil {
		return CaptureResult{}, err
	}
	return res, nil
}
