-- 000069_users_name_trgm.up.sql
-- L8 (PASS-16): trgm-индекс на users.name для поиска игр по автору
-- (games.name уже имеет idx_games_name_trgm из 000036). Без него условие
-- `OR users.name ILIKE '%..%'` (ведущий wildcard) в листинге/автокомплите
-- игнорирует индексы и планировщик выбирает seq scan при росте таблицы users.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_users_name_trgm ON users USING gin (name gin_trgm_ops);
