-- migrations_squashed/000011_schema_tail.up.sql
-- Дополнение после миграции 000064.

-- ========== 000064_unique_pending_payments_chat_rooms.up.sql ==========
-- DEEP-REVIEW PASS-6: уникальные индексы (pending payments TOCTOU + chat rooms race).
CREATE UNIQUE INDEX IF NOT EXISTS idx_payments_user_pending_amount
    ON payments(user_id, amount_kopecks)
    WHERE status = 'pending';

CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_rooms_personal_unique
    ON chat_rooms(user1_id, user2_id)
    WHERE room_type = 'personal' AND deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_rooms_game_general_unique
    ON chat_rooms(game_id)
    WHERE room_type = 'game_general' AND team_id IS NULL AND deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_rooms_team_unique
    ON chat_rooms(game_id, team_id, passing_id)
    WHERE room_type = 'team_general' AND deleted_at IS NULL;
