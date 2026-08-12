-- 000065_tournament_scored_points.down.sql
-- DEEP-REVIEW PASS-6 M4: откат.
ALTER TABLE game_passings DROP COLUMN IF EXISTS tournament_scored_points;
