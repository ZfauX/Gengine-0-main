-- 000051_add_performance_indexes_13.up.sql
-- Индексы из pass 44:

-- S-44-1: ExternalLogin(provider, external_id) должен быть UNIQUE — FindOrCreate
-- (FirstOrCreate) — проверка-затем-вставка; два параллельных OAuth-флоу с одним
-- VK user_id могли создать дубликаты, указывающие на разных пользователей.
-- ВАЖНО: перед применением убедиться, что дубликатов нет (SELECT provider,
-- external_id, count(*) FROM external_logins GROUP BY 1,2 HAVING count(*)>1;).
CREATE UNIQUE INDEX IF NOT EXISTS idx_external_logins_provider_external_unique
    ON external_logins(provider, external_id);

-- P-44-6: GetOpenVotingSession/StartVoting фильтруют
-- (game_passing_id, level_id, is_open) — раздельные idx_blackbox_voting_sessions_*
-- не покрывают выборку.
CREATE INDEX IF NOT EXISTS idx_blackbox_voting_sessions_passing_level
    ON blackbox_voting_sessions(game_passing_id, level_id);
-- NB (P-45-4, pass 45): co_authors composite НЕ добавляем — GORM AutoMigrate уже
-- создаёт uniqueIndex idx_game_user(GameID, UserID) (model.go:132-133), покрывающий
-- FindByGameAndUser. Миграция 000051 из pass 44 содержала дубль — удалён.
