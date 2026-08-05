-- 000028_deferrable_level_position.down.sql
-- Возвращаем обычный (не DEFERRABLE) unique-констрейнт на (game_id, position).
-- Ищем и удаляем constraint по фактическому имени (C5).
DO $$
DECLARE
    constraint_name TEXT;
    game_id_attnum  INTEGER;
    position_attnum INTEGER;
BEGIN
    SELECT attnum INTO game_id_attnum FROM pg_attribute
    WHERE attrelid = 'levels'::regclass AND attname = 'game_id';
    SELECT attnum INTO position_attnum FROM pg_attribute
    WHERE attrelid = 'levels'::regclass AND attname = 'position';

    SELECT conname INTO constraint_name FROM pg_constraint
    WHERE conrelid = 'levels'::regclass AND contype = 'u'
      AND conkey = ARRAY[game_id_attnum, position_attnum]
    ORDER BY conname LIMIT 1;

    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE levels DROP CONSTRAINT %I', constraint_name);
    END IF;
END $$;

ALTER TABLE levels ADD CONSTRAINT levels_game_id_position_key UNIQUE (game_id, position);
