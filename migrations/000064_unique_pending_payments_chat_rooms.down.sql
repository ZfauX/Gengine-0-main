-- 000064_unique_pending_payments_chat_rooms.down.sql
-- DEEP-REVIEW PASS-6: откат уникальных индексов.
DROP INDEX IF EXISTS idx_payments_user_pending_amount;
DROP INDEX IF EXISTS idx_chat_rooms_personal_unique;
DROP INDEX IF EXISTS idx_chat_rooms_game_general_unique;
DROP INDEX IF EXISTS idx_chat_rooms_team_unique;
