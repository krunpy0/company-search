CREATE TABLE accounts.model_token_usage (
    user_id bigint NOT NULL REFERENCES accounts.users(id) ON DELETE CASCADE,
    day date NOT NULL,
    used bigint NOT NULL CHECK (used >= 0),
    PRIMARY KEY (user_id, day)
);
