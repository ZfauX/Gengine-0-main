-- 000067_chat_rooms_unique_remaining.up.sql
-- M4 (PASS-8): уникальные индексы для комнат чата, оставшихся без покрытия
-- в 000064 (game_captains, team_flood). Раньше GetOrCreateCaptainsRoom /
-- GetOrCreateTeamFloodRoom полагались на "нет индекса" — два параллельных
-- входа создавали ДУБЛИКАТЫ комнат (гонка на ChatRoomIDs).
CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_rooms_captains_unique
    ON chat_rooms(game_id)
    WHERE room_type = 'game_captains' AND team_id IS NULL AND deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_rooms_team_flood_unique
    ON chat_rooms(game_id, team_id, passing_id)
    WHERE room_type = 'team_flood' AND deleted_at IS NULL;
