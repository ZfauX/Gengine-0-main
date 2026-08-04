-- 000025_add_tournament_scored.down.sql
ALTER TABLE game_passings DROP COLUMN IF EXISTS tournament_scored;
