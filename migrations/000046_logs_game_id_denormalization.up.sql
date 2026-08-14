-- 000046_logs_game_id_denormalization.up.sql
-- P-5 (pass 39): денормализация logs.game_id.
-- GetLogsByGameID/GetLogsByGameIDPaginated фильтруют по game_id через JOIN
-- game_passings, а ORDER BY logs.created_at поперёк всех passings игры не
-- покрыт индексом (heap-sort на больших играх). Добавляем колонку game_id,
-- backfill и составной индекс (game_id, created_at DESC).

ALTER TABLE logs ADD COLUMN IF NOT EXISTS game_id bigint NOT NULL DEFAULT 0;

-- Backfill из game_passings.
UPDATE logs SET game_id = gp.game_id
FROM game_passings gp
WHERE gp.id = logs.game_passing_id AND logs.game_id = 0;

CREATE INDEX IF NOT EXISTS idx_logs_game_created
    ON logs(game_id, created_at DESC);
