-- 000045_add_performance_indexes_8.up.sql
-- Индексы из pass 37-38 (секция B, P-7 / P-2):

-- Голосование «чёрного ящика»: подзапрос level_progresses по (game_passing_id, level_id)
-- в monitor/service.go Vote — без композитного индекса секвес-скан на партиции прохождения.
-- P-2 (pass 38) + S-1 (pass 39): UNIQUE с WHERE deleted_at IS NULL — автостарт создаёт
-- прогресс первого уровня с ON CONFLICT (game_passing_id, level_id). Частичный индекс
-- не блокирует повторное создание после soft-delete прогресса и не падает, если в БД
-- уже есть дубликаты среди удалённых строк.
CREATE UNIQUE INDEX IF NOT EXISTS idx_level_progresses_passing_level
    ON level_progresses(game_passing_id, level_id) WHERE deleted_at IS NULL;
