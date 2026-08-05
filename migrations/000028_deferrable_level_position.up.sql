-- 000028_deferrable_level_position.up.sql
-- Уникальность (game_id, position) становится DEFERRABLE INITIALLY DEFERRED:
-- перемещения/сдвиги позиций уровней (Move/Duplicate) не падают на транзиентных
-- коллизиях внутри одной транзакции — проверка выполняется при COMMIT (B7/B8).
ALTER TABLE levels DROP CONSTRAINT IF EXISTS levels_game_id_position_key;
ALTER TABLE levels ADD CONSTRAINT levels_game_id_position_key UNIQUE (game_id, position) DEFERRABLE INITIALLY DEFERRED;
