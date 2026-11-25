\set ON_ERROR_STOP on
BEGIN;
GRANT USAGE ON SCHEMA public TO company_api, company_importer;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO company_api;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO company_importer;
REVOKE ALL ON public.schema_migrations FROM company_api, company_importer;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO company_importer;

REVOKE ALL ON SCHEMA accounts FROM company_importer;
REVOKE ALL ON ALL TABLES IN SCHEMA accounts FROM company_importer;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA accounts FROM company_importer;
GRANT USAGE ON SCHEMA accounts TO company_api;
GRANT SELECT, INSERT ON accounts.users TO company_api;
GRANT SELECT, INSERT, DELETE ON accounts.sessions TO company_api;
GRANT SELECT, INSERT, UPDATE ON accounts.external_usage, accounts.external_credentials TO company_api;
GRANT SELECT, INSERT, UPDATE ON accounts.chat_dialogues, accounts.chat_message_usage, accounts.model_token_usage TO company_api;
GRANT SELECT, INSERT ON accounts.chat_turns TO company_api;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA accounts TO company_api;
COMMIT;
