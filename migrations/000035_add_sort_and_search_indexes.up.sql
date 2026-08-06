-- 000035_add_sort_and_search_indexes.up.sql
-- Индексы для сортировки листинга игр по прекомпьютированным агрегатам (P-7)
-- и для LOWER() LIKE-поиска пользователей (P-8, trgm индексы 000023 — case-sensitive).
CREATE INDEX IF NOT EXISTS idx_games_rating_value
    ON games(is_draft, visibility, rating_value DESC);
CREATE INDEX IF NOT EXISTS idx_games_participant_count
    ON games(is_draft, visibility, participant_count DESC);

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_users_name_lower_trgm
    ON users USING gin (lower(name) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_users_email_lower_trgm
    ON users USING gin (lower(email) gin_trgm_ops);
