-- 000002_indexes.up.sql
-- Дополнительные индексы для производительности

-- Составной индекс для фильтрации игр по автору и статусу
CREATE INDEX IF NOT EXISTS idx_games_author_status ON games(author_id, is_draft);

-- Индекс для фильтрации по is_draft и visibility
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility ON games(is_draft, visibility);

-- Составной индекс для сортировки по дате создания с учётом фильтров
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility_created 
ON games(is_draft, visibility, created_at DESC);

-- Индекс для game_passings по game_id и status (участники)
CREATE INDEX IF NOT EXISTS idx_game_passings_game_status ON game_passings(game_id, status);

-- Частичный индекс для level_progresses (активные прохождения)
CREATE INDEX IF NOT EXISTS idx_level_progresses_game_passing_finished 
ON level_progresses(game_passing_id, finished_at) 
WHERE finished_at IS NULL;

-- Составной индекс для истории попыток
CREATE INDEX IF NOT EXISTS idx_attempts_level_progress_created 
ON attempts(level_progress_id, created_at DESC);

-- Индекс для достижений пользователя
CREATE INDEX IF NOT EXISTS idx_user_achievements_user_id ON user_achievements(user_id);
