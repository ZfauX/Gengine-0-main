-- 000051_add_performance_indexes_13.up.sql
-- Индексы из pass 44:

-- S-44-1: ExternalLogin(provider, external_id) должен быть UNIQUE — FindOrCreate
-- (FirstOrCreate) — проверка-затем-вставка; два параллельных OAuth-флоу с одним
-- VK user_id могли создать дубликаты, указывающие на разных пользователей.
-- ВАЖНО: перед применением убедиться, что дубликатов нет (SELECT provider,
-- external_id, count(*) FROM external_logins GROUP BY 1,2 HAVING count(*)>1;).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_external_logins_provider_external_unique
    ON external_logins(provider, external_id);

-- P-44-6: GetOpenVotingSession/StartVoting фильтруют
-- (game_passing_id, level_id, is_open) — раздельные idx_blackbox_voting_sessions_*
-- не покрывают выборку.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_blackbox_voting_sessions_passing_level
    ON blackbox_voting_sessions(game_passing_id, level_id);

-- P-44-4 (доп): FindByGameAndUser фильтрует co_authors(game_id, user_id) —
-- раздельных индексов нет, выборка по обеим колонкам делала bitmap-комбинацию.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_co_authors_game_user
    ON co_authors(game_id, user_id);
