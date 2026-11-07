-- Defence in depth for "append-only" (spec 4.2): in addition to the triggers
-- in 000002, the application role can never UPDATE/DELETE/TRUNCATE the journal
-- or entries. Create the role before migrating (migrations run as the owner):
--   CREATE ROLE ledger_app LOGIN PASSWORD '...';
-- When the role does not exist (e.g. local dev as superuser) this is a no-op.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ledger_app') THEN
        GRANT USAGE ON SCHEMA ledger TO ledger_app;
        GRANT SELECT, INSERT ON ledger.journals, ledger.entries TO ledger_app;
        GRANT SELECT, INSERT, UPDATE ON ledger.accounts, ledger.holds TO ledger_app;
    END IF;
END
$$;
