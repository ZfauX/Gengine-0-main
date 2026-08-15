-- 000071_users_email_trgm.up.sql
-- L17 (PASS-17): trgm-индекс на users.email для ILIKE '%q%' поиска
-- (SearchUsersLight, admin search, autocomplete). Без него ветка
-- `email ILIKE '%..%'` (ведущий wildcard) даёт seq-скан users.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_users_email_trgm ON users USING gin (email gin_trgm_ops);
