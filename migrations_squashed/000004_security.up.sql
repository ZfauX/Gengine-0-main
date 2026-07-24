-- 000004_security.up.sql
-- Безопасность: 2FA, хеширование токенов, блокировка аккаунта, сброс пароля

-- ========== 2FA (TOTP + backup codes) ==========
ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_backup_codes TEXT NOT NULL DEFAULT '';

-- ========== Хеширование токенов (zero-downtime) ==========
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS token_hash TEXT;
ALTER TABLE email_verification_tokens ADD COLUMN IF NOT EXISTS token_hash TEXT;

-- ========== Блокировка при неудачных попытках входа ==========
ALTER TABLE users ADD COLUMN IF NOT EXISTS failed_login_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS locked_until TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_users_locked_until ON users(locked_until);

-- ========== Сброс пароля: код и отметка использования ==========
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS reset_code VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE password_reset_tokens ADD COLUMN IF NOT EXISTS used_at TIMESTAMP WITH TIME ZONE;
CREATE UNIQUE INDEX IF NOT EXISTS idx_password_reset_tokens_reset_code ON password_reset_tokens(reset_code);
CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_used_at ON password_reset_tokens(used_at);
