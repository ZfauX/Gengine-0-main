-- 000019_add_refresh_tokens_missing_columns.up.sql
-- Добавление колонок, отсутствующих в refresh_tokens, но присутствующих в GORM-модели

ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_updated_at ON refresh_tokens(updated_at);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_deleted_at ON refresh_tokens(deleted_at);
