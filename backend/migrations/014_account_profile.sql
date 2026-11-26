ALTER TABLE accounts.users
    ADD COLUMN display_name text NOT NULL DEFAULT '' CHECK (char_length(display_name) <= 80),
    ADD COLUMN avatar bytea CHECK (octet_length(avatar) BETWEEN 1 AND 2097152);

CREATE INDEX sessions_owner ON accounts.sessions (user_id);
