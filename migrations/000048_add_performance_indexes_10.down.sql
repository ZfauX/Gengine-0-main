-- 000048_add_performance_indexes_10.down.sql
DROP INDEX IF EXISTS idx_attempts_progress_is_file_code;
DROP INDEX IF EXISTS idx_attempts_progress_is_file_path;
DROP INDEX IF EXISTS idx_team_members_user_id;
DROP INDEX IF EXISTS idx_games_draft_visibility_created;
