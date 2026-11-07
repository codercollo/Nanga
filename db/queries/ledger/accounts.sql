-- name: InsertAccount :exec
INSERT INTO ledger.accounts (
    id, kind, code, owner_ref, currency, normal_side, allow_negative,
    status, balance, held, version, created_at
) VALUES (
    $1, $2, sqlc.narg('code'), sqlc.narg('owner_ref'), $3, $4, $5,
    $6, $7, $8, $9, $10
);

-- name: GetAccount :one
SELECT * FROM ledger.accounts WHERE id = $1;

-- name: GetAccountForUpdate :one
SELECT * FROM ledger.accounts WHERE id = $1 FOR UPDATE;

-- name: GetWalletByOwner :one
SELECT * FROM ledger.accounts
WHERE kind = 'wallet' AND owner_ref = $1 AND currency = $2;

-- name: GetAccountByCode :one
SELECT * FROM ledger.accounts WHERE code = $1;

-- name: ListAccounts :many
SELECT * FROM ledger.accounts WHERE id > $1 ORDER BY id LIMIT $2;

-- name: UpdateAccountState :execrows
UPDATE ledger.accounts
SET balance = $2, held = $3, status = $4, version = $5
WHERE id = $1 AND version = $6;
