-- 000005_add_trgm_index.up.sql
-- Добавление расширения pg_trgm и GIN-индекса для полнотекстового поиска по названию игр

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_games_name_trgm ON games USING gin (name gin_trgm_ops);