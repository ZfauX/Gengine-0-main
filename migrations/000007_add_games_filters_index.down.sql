-- 000007_add_games_filters_index.down.sql
-- Откат индексов

DROP INDEX IF EXISTS idx_games_draft_visibility;
DROP INDEX IF EXISTS idx_reviews_game_id;
DROP INDEX IF EXISTS idx_game_passings_game_status;
