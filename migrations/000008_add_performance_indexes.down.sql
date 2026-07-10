-- migrations/000008_add_performance_indexes.down.sql
-- Откат индексов производительности

DROP INDEX IF EXISTS idx_level_progresses_game_passing_finished;
DROP INDEX IF EXISTS idx_games_draft_visibility_created;
DROP INDEX IF EXISTS idx_attempts_level_progress_created;
DROP INDEX IF EXISTS idx_game_passings_game_status;
