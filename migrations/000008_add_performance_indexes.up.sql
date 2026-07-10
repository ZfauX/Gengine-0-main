-- migrations/000008_add_performance_indexes.up.sql
-- Индексы для улучшения производительности

-- Индекс для level_progresses (используется в GetCurrentProgress)
CREATE INDEX IF NOT EXISTS idx_level_progresses_game_passing_finished 
ON level_progresses(game_passing_id, finished_at) 
WHERE finished_at IS NULL;

-- Индекс для игр (используется в ListFilteredPaginated)
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility_created 
ON games(is_draft, visibility, created_at DESC);

-- Индекс для attempts (используется в истории попыток)
CREATE INDEX IF NOT EXISTS idx_attempts_level_progress_created 
ON attempts(level_progress_id, created_at DESC);

-- Индекс для game_passings (используется в мониторинге)
CREATE INDEX IF NOT EXISTS idx_game_passings_game_status 
ON game_passings(game_id, status);
