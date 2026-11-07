DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ledger_app') THEN
        REVOKE ALL ON ledger.journals, ledger.entries, ledger.accounts, ledger.holds FROM ledger_app;
        REVOKE USAGE ON SCHEMA ledger FROM ledger_app;
    END IF;
END
$$;
