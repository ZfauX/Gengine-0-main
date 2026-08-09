-- 000050_add_performance_indexes_12.up.sql
-- Индексы из pass 43 (P-43-3):

-- CheckTimeouts (svc_progress.go) каждые 30с: WHERE finished_at IS NULL
-- ORDER BY started_at ASC LIMIT batch. Существующие partial-индексы
-- (game_passing_id, finished_at) / (game_passing_id, created_at) не покрывают
-- сортировку по started_at незавершённых прогрессов → полная сортировка.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_level_progresses_unfinished_started
    ON level_progresses(started_at) WHERE finished_at IS NULL;
