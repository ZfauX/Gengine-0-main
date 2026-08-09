-- 000047_add_performance_indexes_9.up.sql
-- Индексы из pass 40 (L-3):

-- GetByTournamentAndTeam / GetByTournamentAndTeamIDs фильтруют по
-- (tournament_id, team_id); существующий idx_tournament_results_tournament_score
-- (tournament_id, score DESC) не покрывает выборку по team_id (leading-колонка
-- score). Seq scan при lookup команды в турнире.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_tournament_results_tournament_team
    ON tournament_results(tournament_id, team_id);
