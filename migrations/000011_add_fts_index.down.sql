-- 000011_add_fts_index.down.sql
DROP TRIGGER IF EXISTS trg_games_search_vector ON games;
DROP FUNCTION IF EXISTS games_search_vector_update();
DROP INDEX IF EXISTS idx_games_search_vector;
ALTER TABLE games DROP COLUMN IF EXISTS search_vector;
