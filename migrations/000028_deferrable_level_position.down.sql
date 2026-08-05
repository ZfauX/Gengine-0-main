-- 000028_deferrable_level_position.down.sql
ALTER TABLE levels DROP CONSTRAINT IF EXISTS levels_game_id_position_key;
ALTER TABLE levels ADD CONSTRAINT levels_game_id_position_key UNIQUE (game_id, position);
