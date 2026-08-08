-- 000039_add_performance_indexes_3.down.sql
DROP INDEX IF EXISTS idx_team_members_team_id;
DROP INDEX IF EXISTS idx_game_passings_team_status;
DROP INDEX IF EXISTS idx_level_progresses_passing_unfinished_created;
