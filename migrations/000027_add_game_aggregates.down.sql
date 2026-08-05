-- 000027_add_game_aggregates.down.sql
DROP TRIGGER IF EXISTS trg_refresh_game_participants ON game_passings;
DROP TRIGGER IF EXISTS trg_refresh_game_rating ON reviews;
DROP FUNCTION IF EXISTS refresh_game_participants();
DROP FUNCTION IF EXISTS refresh_game_rating();
ALTER TABLE games DROP COLUMN IF EXISTS participant_count;
ALTER TABLE games DROP COLUMN IF EXISTS rating_value;
