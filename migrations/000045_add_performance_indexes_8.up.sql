-- 000045_add_performance_indexes_8.up.sql
-- Индексы из pass 37 (секция B, P-7):

-- Голосование «чёрного ящика»: подзапрос level_progresses по (game_passing_id, level_id)
-- в monitor/service.go Vote — без композитного индекса секвес-скан на партиции прохождения.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_level_progresses_passing_level
    ON level_progresses(game_passing_id, level_id);
