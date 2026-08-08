-- 000044_add_performance_indexes_7.down.sql
DROP INDEX CONCURRENTLY IF EXISTS idx_notifications_read_at_read;
DROP INDEX CONCURRENTLY IF EXISTS idx_games_visibility_draft_author;
