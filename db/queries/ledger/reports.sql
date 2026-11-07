-- name: EntryTotalsForAccount :one
SELECT
    COALESCE(SUM(amount) FILTER (WHERE side = 'debit'), 0)::bigint  AS debits,
    COALESCE(SUM(amount) FILTER (WHERE side = 'credit'), 0)::bigint AS credits
FROM ledger.entries
WHERE account_id = $1;

-- name: TrialBalance :many
SELECT
    currency,
    COALESCE(SUM(amount) FILTER (WHERE side = 'debit'), 0)::bigint  AS debits,
    COALESCE(SUM(amount) FILTER (WHERE side = 'credit'), 0)::bigint AS credits
FROM ledger.entries
GROUP BY currency
ORDER BY currency;
