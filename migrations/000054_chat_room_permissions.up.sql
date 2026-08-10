-- 000054_chat_room_permissions.up.sql
-- B-1/B-5 (pass 45): типы комнат чата, владелец, членство с правами.
ALTER TABLE chat_rooms
    ADD COLUMN IF NOT EXISTS room_type TEXT NOT NULL DEFAULT 'game_general',
    ADD COLUMN IF NOT EXISTS owner_id INTEGER REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_chat_rooms_type ON chat_rooms(room_type);
CREATE INDEX IF NOT EXISTS idx_chat_rooms_owner ON chat_rooms(owner_id);

CREATE TABLE IF NOT EXISTS chat_room_members (
    room_id INTEGER NOT NULL REFERENCES chat_rooms(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    can_read BOOLEAN NOT NULL DEFAULT TRUE,
    can_write BOOLEAN NOT NULL DEFAULT TRUE,
    can_attach BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (room_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_room_member_user ON chat_room_members(user_id);
