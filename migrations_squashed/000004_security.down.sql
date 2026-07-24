-- 000004_security.down.sql
-- Откат изменений безопасности

-- Сброс пароля
DROP INDEX IF EXISTS idx_password_reset_tokens_used_at;
DROP INDEX IF EXISTS idx_password_reset_tokens_reset_code;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS used_at;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS reset_code;

-- Блокировка аккаунта
DROP INDEX IF EXISTS idx_users_locked_until;
ALTER TABLE users DROP COLUMN IF EXISTS locked_until;
ALTER TABLE users DROP COLUMN IF EXISTS failed_login_attempts;

-- Хеширование токенов (данные не восстанавливаются)
ALTER TABLE email_verification_tokens DROP COLUMN IF EXISTS token_hash;
ALTER TABLE password_reset_tokens DROP COLUMN IF EXISTS token_hash;

-- 2FA
ALTER TABLE users DROP COLUMN IF EXISTS two_factor_backup_codes;
ALTER TABLE users DROP COLUMN IF EXISTS two_factor_secret;
ALTER TABLE users DROP COLUMN IF EXISTS two_factor_enabled;
