-- 000009_add_two_factor_fields.up.sql
-- Добавление полей для 2FA (TOTP + backup codes) в таблицу users

ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS two_factor_backup_codes TEXT NOT NULL DEFAULT '';
