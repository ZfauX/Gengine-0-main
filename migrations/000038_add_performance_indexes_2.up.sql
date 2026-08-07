-- 000038_add_performance_indexes_2.up.sql
-- Индексы из pass 29 (секция B):

-- OAuth: поиск/создание external_login по провайдеру (на каждый вход).
CREATE INDEX IF NOT EXISTS idx_external_logins_provider_external
    ON external_logins(provider, external_id);

-- Удаление пользователя: каскад по user_id.
CREATE INDEX IF NOT EXISTS idx_external_logins_user_id
    ON external_logins(user_id);

-- Лидерборд: ORDER BY score DESC.
CREATE INDEX IF NOT EXISTS idx_player_ratings_score
    ON player_ratings(score DESC);

-- Админ-поиск команд: ILIKE по teams.name (trgm есть для users/games).
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_teams_name_trgm
    ON teams USING gin (name gin_trgm_ops);
