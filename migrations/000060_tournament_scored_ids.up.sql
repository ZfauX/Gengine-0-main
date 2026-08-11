-- 000060_tournament_scored_ids.up.sql
-- DEEP-REVIEW (pass 46): мультитурнирный скоринг.
-- Раньше game_passings.tournament_scored (bool) помечал прохождение начисленным
-- для ВСЕХ турниров сразу — игра в 2+ турнирах теряла очки во втором турнире.
-- Новая колонка хранит массив tournament_id, которым уже начислены очки.
ALTER TABLE game_passings
    ADD COLUMN IF NOT EXISTS tournament_scored_ids BIGINT[] NOT NULL DEFAULT '{}';

-- Переносим существующие начисления: если флаг был true, считаем, что все
-- турниры, где участвует команда прохождения, уже получили очки.
UPDATE game_passings gp
SET tournament_scored_ids = COALESCE((
    SELECT array_agg(DISTINCT tt.tournament_id)
    FROM tournament_teams tt
    WHERE tt.team_id = gp.team_id
), '{}')
WHERE gp.tournament_scored = true AND gp.tournament_scored_ids = '{}';
