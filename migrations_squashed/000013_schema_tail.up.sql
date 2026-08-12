-- migrations_squashed/000013_schema_tail.up.sql
-- Дополнение после миграции 000066.

-- ========== 000066_personal_chat_consent.up.sql ==========
ALTER TABLE chat_rooms ADD COLUMN IF NOT EXISTS accepted BOOLEAN NOT NULL DEFAULT FALSE;
