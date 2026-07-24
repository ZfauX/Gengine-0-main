-- 000003_search.down.sql
-- Откат поисковых индексов

DROP INDEX IF EXISTS idx_games_name_trgm;

DROP TRIGGER IF EXISTS trg_games_search_vector ON games;
DROP FUNCTION IF EXISTS games_search_vector_update();
DROP INDEX IF EXISTS idx_games_search_vector;
ALTER TABLE games DROP COLUMN IF EXISTS search_vector;
