-- 000009_add_two_factor_fields.down.sql
-- Удаление полей 2FA из таблицы users

ALTER TABLE users DROP COLUMN IF EXISTS two_factor_enabled;
ALTER TABLE users DROP COLUMN IF EXISTS two_factor_secret;
ALTER TABLE users DROP COLUMN IF EXISTS two_factor_backup_codes;
