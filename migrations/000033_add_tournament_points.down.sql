-- 000033_add_tournament_points.down.sql
ALTER TABLE game_passings DROP COLUMN IF EXISTS tournament_points;
