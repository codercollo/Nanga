CREATE SCHEMA IF NOT EXISTS ledger;

-- Accounts hold the current balance (minor units, in the account's normal-side
-- convention) and the amount reserved by active holds. `version` is an
-- optimistic guard; correctness comes from row locks taken in id order.
CREATE TABLE ledger.accounts (
    id             uuid        PRIMARY KEY,
    kind           text        NOT NULL CHECK (kind IN ('wallet', 'system')),
    code           text,
    owner_ref      text,
    currency       text        NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    normal_side    text        NOT NULL CHECK (normal_side IN ('debit', 'credit')),
    allow_negative boolean     NOT NULL DEFAULT false,
    status         text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'frozen', 'closed')),
    balance        bigint      NOT NULL DEFAULT 0,
    held           bigint      NOT NULL DEFAULT 0 CHECK (held >= 0),
    version        bigint      NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL,
    CONSTRAINT accounts_kind_identity CHECK (
        (kind = 'wallet' AND owner_ref IS NOT NULL AND code IS NULL) OR
        (kind = 'system' AND code IS NOT NULL AND owner_ref IS NULL)),
    -- Database-level backstop for the core invariant.
    CONSTRAINT accounts_non_negative CHECK (allow_negative OR balance - held >= 0)
);
CREATE UNIQUE INDEX accounts_code_key ON ledger.accounts (code) WHERE code IS NOT NULL;
CREATE UNIQUE INDEX accounts_owner_currency_key ON ledger.accounts (owner_ref, currency) WHERE kind = 'wallet';

CREATE TABLE ledger.journals (
    id              uuid        PRIMARY KEY,
    idempotency_key text        NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    fingerprint     text        NOT NULL,
    kind            text        NOT NULL,
    reference       text        NOT NULL DEFAULT '',
    description     text        NOT NULL DEFAULT '',
    currency        text        NOT NULL,
    posted_at       timestamptz NOT NULL,
    -- A correction is a new journal pointing at the original (never an edit).
    reverses_id     uuid        REFERENCES ledger.journals (id),
    CONSTRAINT journals_idempotency_key_key UNIQUE (idempotency_key),
    CONSTRAINT journals_reverses_id_key UNIQUE (reverses_id),
    CONSTRAINT journals_reversal_kind CHECK ((reverses_id IS NOT NULL) = (kind = 'reversal'))
);
CREATE INDEX journals_reference_idx ON ledger.journals (reference) WHERE reference <> '';

CREATE TABLE ledger.entries (
    seq           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    journal_id    uuid   NOT NULL REFERENCES ledger.journals (id),
    line_no       int    NOT NULL,
    account_id    uuid   NOT NULL REFERENCES ledger.accounts (id),
    side          text   NOT NULL CHECK (side IN ('debit', 'credit')),
    amount        bigint NOT NULL CHECK (amount > 0),
    currency      text   NOT NULL,
    balance_after bigint NOT NULL,
    CONSTRAINT entries_journal_line_key UNIQUE (journal_id, line_no)
);
CREATE INDEX entries_account_seq_idx ON ledger.entries (account_id, seq);

CREATE TABLE ledger.holds (
    id          uuid        PRIMARY KEY,
    account_id  uuid        NOT NULL REFERENCES ledger.accounts (id),
    reference   text        NOT NULL CHECK (length(reference) BETWEEN 1 AND 128),
    amount      bigint      NOT NULL CHECK (amount > 0),
    currency    text        NOT NULL,
    status      text        NOT NULL CHECK (status IN ('active', 'released', 'captured')),
    journal_id  uuid        REFERENCES ledger.journals (id),
    created_at  timestamptz NOT NULL,
    resolved_at timestamptz,
    CONSTRAINT holds_reference_key UNIQUE (reference),
    CONSTRAINT holds_captured_has_journal CHECK ((status = 'captured') = (journal_id IS NOT NULL)),
    CONSTRAINT holds_active_unresolved CHECK ((status = 'active') = (resolved_at IS NULL))
);
CREATE INDEX holds_account_active_idx ON ledger.holds (account_id) WHERE status = 'active';
