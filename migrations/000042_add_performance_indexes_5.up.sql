-- 000042_add_performance_indexes_5.up.sql
-- Индексы из pass 32 (секция B):

-- Список заметок: ORDER BY created_at DESC по игре (note_repository.ListByGame).
CREATE INDEX IF NOT EXISTS idx_notes_game_created
    ON notes(game_id, created_at DESC);

-- Галерея фото: ORDER BY created_at DESC по игре (photo_repository.List).
CREATE INDEX IF NOT EXISTS idx_photos_game_created
    ON photos(game_id, created_at DESC);

-- StartTesting ищет тестовую команду по точному имени (svc_play).
-- Существующий idx_teams_name_trgm (000038) — для ILIKE, не для =.
CREATE INDEX IF NOT EXISTS idx_teams_name
    ON teams(name);
