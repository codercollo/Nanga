-- name: InsertJournal :exec
INSERT INTO ledger.journals (
    id, idempotency_key, fingerprint, kind, reference, description, currency, posted_at, reverses_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, sqlc.narg('reverses_id'));

-- name: InsertEntry :exec
INSERT INTO ledger.entries (
    journal_id, line_no, account_id, side, amount, currency, balance_after
) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetJournalByKey :one
SELECT * FROM ledger.journals WHERE idempotency_key = $1;

-- name: ListJournalEntries :many
SELECT * FROM ledger.entries WHERE journal_id = $1 ORDER BY line_no;

-- name: GetReversalOf :one
SELECT * FROM ledger.journals WHERE reverses_id = $1;

-- name: ListAccountStatement :many
SELECT e.seq, e.journal_id, j.kind, j.reference, j.description,
       e.side, e.amount, e.currency, e.balance_after, j.posted_at
FROM ledger.entries e
JOIN ledger.journals j ON j.id = e.journal_id
WHERE e.account_id = $1 AND e.seq < $2
ORDER BY e.seq DESC
LIMIT $3;
