-- 000059_chat_personal_rooms.down.sql
ALTER TABLE chat_rooms DROP COLUMN IF EXISTS user1_id;
ALTER TABLE chat_rooms DROP COLUMN IF EXISTS user2_id;
