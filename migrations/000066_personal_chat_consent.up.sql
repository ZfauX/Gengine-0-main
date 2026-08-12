-- 000066_personal_chat_consent.up.sql
-- DEEP-REVIEW PASS-6 M7: согласие на личный чат.
-- Комната создаётся инициатором (owner), получатель (user2) должен явно принять
-- переписку — иначе сообщения в его адрес блокируются (закрывает спам-вектор).
ALTER TABLE chat_rooms ADD COLUMN IF NOT EXISTS accepted BOOLEAN NOT NULL DEFAULT FALSE;
