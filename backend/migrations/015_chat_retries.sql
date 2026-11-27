ALTER TABLE accounts.chat_dialogues
    ADD COLUMN title_attempts smallint NOT NULL DEFAULT 0 CHECK (title_attempts BETWEEN 0 AND 2),
    ADD COLUMN title_retry_after timestamptz;
UPDATE accounts.chat_dialogues SET title_attempts = 1 WHERE title_attempted AND title IS NULL;
ALTER TABLE accounts.chat_dialogues DROP COLUMN title_attempted;

ALTER TABLE accounts.chat_turns ADD COLUMN client_message_id uuid;
CREATE UNIQUE INDEX chat_turns_client_message ON accounts.chat_turns (dialogue_id, client_message_id)
    WHERE client_message_id IS NOT NULL;
