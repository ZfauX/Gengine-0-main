-- 000059_chat_personal_rooms.up.sql
-- B-7 (pass 45): личный чат 1-на-1 — поля участников в chat_rooms.
ALTER TABLE chat_rooms
    ADD COLUMN IF NOT EXISTS user1_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
    ADD COLUMN IF NOT EXISTS user2_id BIGINT REFERENCES users(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_chat_rooms_users ON chat_rooms(user1_id, user2_id);
