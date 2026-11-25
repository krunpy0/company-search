CREATE SCHEMA accounts;
REVOKE ALL ON SCHEMA accounts FROM PUBLIC;

CREATE TABLE accounts.users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email text NOT NULL UNIQUE,
    password_hash bytea NOT NULL
);

CREATE TABLE accounts.sessions (
    token_hash bytea PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES accounts.users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);

CREATE TABLE accounts.external_usage (
    user_id bigint NOT NULL REFERENCES accounts.users(id) ON DELETE CASCADE,
    day date NOT NULL,
    used integer NOT NULL CHECK (used > 0),
    PRIMARY KEY (user_id, day)
);

CREATE TABLE accounts.external_credentials (
    key_hash bytea PRIMARY KEY,
    day date NOT NULL,
    used integer NOT NULL DEFAULT 0 CHECK (used >= 0),
    next_at timestamptz NOT NULL DEFAULT '-infinity',
    cooldown_until timestamptz NOT NULL DEFAULT '-infinity'
);
