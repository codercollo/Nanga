-- Journals and entries are append-only: no UPDATE, DELETE or TRUNCATE.
CREATE FUNCTION ledger.forbid_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ledger.% is append-only (% not allowed)', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$;

CREATE TRIGGER journals_append_only BEFORE UPDATE OR DELETE ON ledger.journals
    FOR EACH ROW EXECUTE FUNCTION ledger.forbid_mutation();
CREATE TRIGGER entries_append_only BEFORE UPDATE OR DELETE ON ledger.entries
    FOR EACH ROW EXECUTE FUNCTION ledger.forbid_mutation();
CREATE TRIGGER journals_no_truncate BEFORE TRUNCATE ON ledger.journals
    FOR EACH STATEMENT EXECUTE FUNCTION ledger.forbid_mutation();
CREATE TRIGGER entries_no_truncate BEFORE TRUNCATE ON ledger.entries
    FOR EACH STATEMENT EXECUTE FUNCTION ledger.forbid_mutation();
CREATE TRIGGER accounts_no_delete BEFORE DELETE ON ledger.accounts
    FOR EACH ROW EXECUTE FUNCTION ledger.forbid_mutation();
CREATE TRIGGER holds_no_delete BEFORE DELETE ON ledger.holds
    FOR EACH ROW EXECUTE FUNCTION ledger.forbid_mutation();

-- A journal must balance (debits = credits) at commit time. Deferred so all
-- of a journal's entries can be inserted first.
CREATE FUNCTION ledger.assert_journal_balanced() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    d bigint;
    c bigint;
BEGIN
    SELECT COALESCE(SUM(amount) FILTER (WHERE side = 'debit'), 0),
           COALESCE(SUM(amount) FILTER (WHERE side = 'credit'), 0)
      INTO d, c
      FROM ledger.entries WHERE journal_id = NEW.journal_id;
    IF d <> c THEN
        RAISE EXCEPTION 'journal % is unbalanced: debits % credits %', NEW.journal_id, d, c
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER entries_journal_balanced
    AFTER INSERT ON ledger.entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION ledger.assert_journal_balanced();

-- Account identity is immutable; only balance, held, status, version change.
CREATE FUNCTION ledger.guard_account_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.kind <> OLD.kind OR NEW.currency <> OLD.currency
       OR NEW.normal_side <> OLD.normal_side OR NEW.allow_negative <> OLD.allow_negative
       OR NEW.code IS DISTINCT FROM OLD.code OR NEW.owner_ref IS DISTINCT FROM OLD.owner_ref
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'ledger.accounts identity columns are immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER accounts_guard_update BEFORE UPDATE ON ledger.accounts
    FOR EACH ROW EXECUTE FUNCTION ledger.guard_account_update();

-- Holds resolve once: active -> released | captured, never back.
CREATE FUNCTION ledger.guard_hold_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status <> 'active' THEN
        RAISE EXCEPTION 'hold % is already %', OLD.reference, OLD.status
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NEW.id <> OLD.id OR NEW.account_id <> OLD.account_id OR NEW.reference <> OLD.reference
       OR NEW.amount <> OLD.amount OR NEW.currency <> OLD.currency THEN
        RAISE EXCEPTION 'hold identity columns are immutable'
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER holds_guard_update BEFORE UPDATE ON ledger.holds
    FOR EACH ROW EXECUTE FUNCTION ledger.guard_hold_update();
