-- 000014_add_failed_login_fields.up.sql
-- Добавление полей для блокировки аккаунта при неудачных попытках входа

ALTER TABLE users ADD COLUMN IF NOT EXISTS failed_login_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS locked_until TIMESTAMP WITH TIME ZONE;
CREATE INDEX IF NOT EXISTS idx_users_locked_until ON users(locked_until);
