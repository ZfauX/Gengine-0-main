-- 000055_team_routes_and_answers.down.sql
DROP TABLE IF EXISTS level_team_answers;
DROP TABLE IF EXISTS game_passing_levels;
ALTER TABLE game_passings DROP COLUMN IF EXISTS start_time;
ALTER TABLE attempts DROP COLUMN IF EXISTS user_id;
