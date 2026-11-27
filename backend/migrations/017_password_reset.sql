ALTER TABLE accounts.users
    ADD COLUMN reset_token_hash bytea UNIQUE,
    ADD COLUMN reset_expires_at timestamptz;

DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'company_api') THEN
        GRANT UPDATE (reset_token_hash, reset_expires_at) ON accounts.users TO company_api;
    END IF;
END
$$;
