-- 000019_add_refresh_tokens_missing_columns.down.sql
-- Откат: удаление колонок, добавленных в 000019

DROP INDEX IF EXISTS idx_refresh_tokens_updated_at;
DROP INDEX IF EXISTS idx_refresh_tokens_deleted_at;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS updated_at;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS deleted_at;
