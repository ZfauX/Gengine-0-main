-- 000038_add_performance_indexes_2.down.sql
DROP INDEX IF EXISTS idx_external_logins_provider_external;
DROP INDEX IF EXISTS idx_external_logins_user_id;
DROP INDEX IF EXISTS idx_player_ratings_score;
DROP INDEX IF EXISTS idx_teams_name_trgm;
