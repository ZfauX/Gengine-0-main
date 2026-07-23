-- 000013_add_trgm_index.down.sql
-- Откат: удаление индекса и расширения

DROP INDEX IF EXISTS idx_games_name_trgm;
-- Расширение pg_trgm не удаляем, так как оно может использоваться в других местах
-- DROP EXTENSION IF EXISTS pg_trgm;