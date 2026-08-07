-- 000037_add_performance_indexes.up.sql
-- Индексы из pass 28 (секция B, PF5/PF6 и рекомендованные):

-- Лидерборд турнира: ORDER BY score DESC по турниру (filesort на данных без индекса).
CREATE INDEX IF NOT EXISTS idx_tournament_results_tournament_score
    ON tournament_results(tournament_id, score DESC);

-- Чат: выборка истории комнаты ORDER BY created_at DESC (filesort на каждом открытии).
CREATE INDEX IF NOT EXISTS idx_chat_messages_room_created
    ON chat_messages(room_id, created_at DESC);

-- Timeout-воркер: незавершённые прогрессы (finished_at IS NULL) по времени старта.
CREATE INDEX IF NOT EXISTS idx_level_progresses_unfinished_started
    ON level_progresses(started_at) WHERE finished_at IS NULL;

-- Листинг игр: фильтр по статусу/видимости + сортировка по названию.
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility_name
    ON games(is_draft, visibility, name);

-- Календарь/листинг: фильтр по статусу/видимости + время старта.
CREATE INDEX IF NOT EXISTS idx_games_draft_visibility_starts
    ON games(is_draft, visibility, starts_at);

-- Отзывы на странице игры: ORDER BY created_at DESC.
CREATE INDEX IF NOT EXISTS idx_reviews_game_created
    ON reviews(game_id, created_at DESC);
