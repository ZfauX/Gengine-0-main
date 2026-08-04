-- 000021_add_client_fingerprint.up.sql
-- Добавляет колонку client_fingerprint в refresh_tokens для token binding (M7).

ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS client_fingerprint TEXT NOT NULL DEFAULT '';
