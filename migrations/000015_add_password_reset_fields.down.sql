-- 000015_add_password_reset_fields.down.sql
-- Откат: удаление полей reset_code и used_at

DROP INDEX IF EXISTS idx_password_reset_tokens_used_at;
DROP INDEX IF EXISTS idx_password_reset_tokens_reset_code;

ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS used_at;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS reset_code;
