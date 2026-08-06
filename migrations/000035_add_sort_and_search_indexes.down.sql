-- 000035_add_sort_and_search_indexes.down.sql
DROP INDEX IF EXISTS idx_games_rating_value;
DROP INDEX IF EXISTS idx_games_participant_count;
DROP INDEX IF EXISTS idx_users_name_lower_trgm;
DROP INDEX IF EXISTS idx_users_email_lower_trgm;
