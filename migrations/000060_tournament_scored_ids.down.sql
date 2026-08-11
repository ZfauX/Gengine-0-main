-- 000060_tournament_scored_ids.down.sql
ALTER TABLE game_passings DROP COLUMN IF EXISTS tournament_scored_ids;
