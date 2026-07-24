-- 000002_indexes.down.sql
-- Удаление дополнительных индексов

DROP INDEX IF EXISTS idx_user_achievements_user_id;
DROP INDEX IF EXISTS idx_attempts_level_progress_created;
DROP INDEX IF EXISTS idx_level_progresses_game_passing_finished;
DROP INDEX IF EXISTS idx_game_passings_game_status;
DROP INDEX IF EXISTS idx_games_draft_visibility_created;
DROP INDEX IF EXISTS idx_games_draft_visibility;
DROP INDEX IF EXISTS idx_games_author_status;
