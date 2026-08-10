-- 000054_chat_room_permissions.down.sql
DROP TABLE IF EXISTS chat_room_members;
DROP INDEX IF EXISTS idx_chat_rooms_type;
DROP INDEX IF EXISTS idx_chat_rooms_owner;
ALTER TABLE chat_rooms DROP COLUMN IF EXISTS room_type;
ALTER TABLE chat_rooms DROP COLUMN IF EXISTS owner_id;
