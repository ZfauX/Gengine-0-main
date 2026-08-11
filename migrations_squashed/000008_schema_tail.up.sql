-- migrations_squashed/000008_schema_tail.up.sql
-- Полный слепок изменений 000060-000061.

-- ========== 000060_tournament_scored_ids.up.sql ==========
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

-- ========== 000061_team_members_user_unique.up.sql ==========
-- 000061_team_members_user_unique.up.sql
-- DEEP-REVIEW (pass 46): инвариант «игрок в одной команде» (A-5) теперь
-- гарантируется на уровне БД. Раньше проверка в сервисе была TOCTOU:
-- AcceptInvitation проверял GetTeamsByUserID ВНЕ транзакции, а INSERT
-- защищался только ON CONFLICT (team_id, user_id) — два конкурентных
-- accept могли поставить игрока в две команды.
-- team_members не использует soft-delete (primaryKey = team_id+user_id),
-- поэтому уникальный индекс на user_id безопасен.
CREATE UNIQUE INDEX IF NOT EXISTS idx_team_members_user_unique
    ON team_members(user_id);

