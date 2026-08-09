-- 000046_logs_game_id_denormalization.down.sql
DROP INDEX IF EXISTS idx_logs_game_created;
ALTER TABLE logs DROP COLUMN IF EXISTS game_id;
