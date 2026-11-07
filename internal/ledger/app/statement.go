package app

import (
	"context"

	"github.com/codercollo/nanga/internal/ledger/domain"
)

const (
	defaultStatementLimit = 50
	maxStatementLimit     = 200
)

// StatementPage is one page of account history, newest first. NextBefore is
// the cursor for the following page (0 when there are no more lines).
type StatementPage struct {
	Lines      []domain.StatementLine
	NextBefore int64
}

// Statement returns cursor-paginated history for an account. Pass
// beforeSeq = 0 for the first page, then the previous page's NextBefore.
// limit is clamped to [1, 200] (default 50).
func (s *Service) Statement(ctx context.Context, accountID string, beforeSeq int64, limit int) (StatementPage, error) {
	ctx, cancel := s.bound(ctx)
	defer cancel()
	if limit <= 0 {
		limit = defaultStatementLimit
	}
	if limit > maxStatementLimit {
		limit = maxStatementLimit
	}
	if _, err := s.repo.GetAccount(ctx, accountID); err != nil {
		return StatementPage{}, err
	}
	lines, err := s.repo.ListStatement(ctx, accountID, beforeSeq, limit+1)
	if err != nil {
		return StatementPage{}, err
	}
	page := StatementPage{Lines: lines}
	if len(lines) > limit {
		page.Lines = lines[:limit]
		page.NextBefore = lines[limit-1].Seq
	}
	return page, nil
}
