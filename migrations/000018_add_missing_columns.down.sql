-- 000018_add_missing_columns.down.sql
-- Откат: удаление колонок, добавленных в 000018.

-- ========== email_verification_tokens ==========
DROP INDEX IF EXISTS idx_email_verification_tokens_verification_code;
DROP INDEX IF EXISTS idx_email_verification_tokens_deleted_at;
ALTER TABLE email_verification_tokens DROP COLUMN IF EXISTS verification_code;
ALTER TABLE email_verification_tokens DROP COLUMN IF EXISTS updated_at;
ALTER TABLE email_verification_tokens DROP COLUMN IF EXISTS deleted_at;

-- ========== password_reset_tokens ==========
DROP INDEX IF EXISTS idx_password_reset_tokens_deleted_at;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS updated_at;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS deleted_at;

-- ========== notifications ==========
DROP INDEX IF EXISTS idx_notifications_deleted_at;
ALTER TABLE notifications DROP COLUMN IF EXISTS updated_at;
ALTER TABLE notifications DROP COLUMN IF EXISTS deleted_at;
