-- 000015_add_password_reset_fields.up.sql
-- Добавление полей reset_code и used_at в таблицу password_reset_tokens

ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS reset_code VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS used_at TIMESTAMP WITH TIME ZONE;

CREATE UNIQUE INDEX IF NOT EXISTS idx_password_reset_tokens_reset_code ON password_reset_tokens(reset_code);
CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_used_at ON password_reset_tokens(used_at);
