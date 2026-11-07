package domain

import (
	"time"

	"github.com/codercollo/nanga/internal/platform/money"
)

// StatementLine is one posted entry on an account, joined with its journal,
// for wallet history. Seq is a globally increasing cursor.
type StatementLine struct {
	Seq          int64
	JournalID    string
	Kind         string
	Reference    string
	Description  string
	Side         Side
	Amount       money.Money
	BalanceAfter int64
	PostedAt     time.Time
}
