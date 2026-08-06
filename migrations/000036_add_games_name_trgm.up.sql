-- 000036_add_games_name_trgm.up.sql
-- pg_trgm-индекс на games.name для ILIKE-поиска (pass 26 / autocomplete + admin):
-- без него `%..%` поиск сканирует всю таблицу.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_games_name_trgm ON games USING gin (name gin_trgm_ops);
