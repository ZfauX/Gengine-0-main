-- 000021_add_client_fingerprint.down.sql
-- Удаляет колонку client_fingerprint из refresh_tokens.

ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS client_fingerprint;
