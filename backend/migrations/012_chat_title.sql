ALTER TABLE accounts.chat_dialogues ADD COLUMN title text CHECK (char_length(title) BETWEEN 1 AND 80);
