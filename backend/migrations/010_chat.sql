CREATE TABLE accounts.chat_dialogues (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES accounts.users(id) ON DELETE CASCADE,
    registration text CHECK (registration ~ '^(reg:)?([0-9]{13}|[0-9]{15})$'),
    pending_question text,
    pending_registrations text[],
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    lease_id text,
    lease_until timestamptz,
    CHECK ((pending_question IS NULL) = (pending_registrations IS NULL)),
    CHECK ((lease_id IS NULL) = (lease_until IS NULL))
);
CREATE INDEX chat_dialogues_owner ON accounts.chat_dialogues (user_id, id DESC);

CREATE TABLE accounts.chat_turns (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    dialogue_id bigint NOT NULL REFERENCES accounts.chat_dialogues(id) ON DELETE CASCADE,
    question text NOT NULL,
    answer jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp()
);
CREATE INDEX chat_turns_dialogue ON accounts.chat_turns (dialogue_id, id DESC);

CREATE TABLE accounts.chat_message_usage (
    user_id bigint NOT NULL REFERENCES accounts.users(id) ON DELETE CASCADE,
    day date NOT NULL,
    used integer NOT NULL CHECK (used BETWEEN 1 AND 5),
    PRIMARY KEY (user_id, day)
);
