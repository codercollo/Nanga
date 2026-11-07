-- name: InsertHold :exec
INSERT INTO ledger.holds (
    id, account_id, reference, amount, currency, status, journal_id, created_at, resolved_at
) VALUES ($1, $2, $3, $4, $5, $6, sqlc.narg('journal_id'), $7, sqlc.narg('resolved_at'));

-- name: GetHoldByRef :one
SELECT * FROM ledger.holds WHERE reference = $1;

-- name: ResolveHold :execrows
UPDATE ledger.holds
SET status = $2, journal_id = sqlc.narg('journal_id'), resolved_at = $3
WHERE reference = $1 AND status = 'active';
