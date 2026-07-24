-- 000014_add_failed_login_fields.down.sql
-- Откат: удаление полей блокировки

DROP INDEX IF EXISTS idx_users_locked_until;
ALTER TABLE users DROP COLUMN IF EXISTS locked_until;
ALTER TABLE users DROP COLUMN IF EXISTS failed_login_attempts;
