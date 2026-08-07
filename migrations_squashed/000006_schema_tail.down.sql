-- migrations_squashed/000006_schema_tail.down.sql

-- ========== 000036_add_games_name_trgm.down.sql ==========
-- 000036_add_games_name_trgm.down.sql
DROP INDEX IF EXISTS idx_games_name_trgm;

-- ========== 000035_add_sort_and_search_indexes.down.sql ==========
-- 000035_add_sort_and_search_indexes.down.sql
DROP INDEX IF EXISTS idx_games_rating_value;
DROP INDEX IF EXISTS idx_games_participant_count;
DROP INDEX IF EXISTS idx_users_name_lower_trgm;
DROP INDEX IF EXISTS idx_users_email_lower_trgm;

-- ========== 000034_unique_reviews.down.sql ==========
-- 000034_unique_reviews.down.sql
DROP INDEX IF EXISTS idx_reviews_game_user;

-- ========== 000033_add_tournament_points.down.sql ==========
-- 000033_add_tournament_points.down.sql
ALTER TABLE game_passings DROP COLUMN IF EXISTS tournament_points;

-- ========== 000032_notifications_user_created.down.sql ==========
-- 000032_notifications_user_created.down.sql
DROP INDEX IF EXISTS idx_notifications_user_created;

-- ========== 000031_voting_sessions_unique.down.sql ==========
-- 000031_voting_sessions_unique.down.sql
DROP INDEX IF EXISTS idx_passing_level;

-- ========== 000030_add_rating_scored.down.sql ==========
-- 000030_add_rating_scored.down.sql
ALTER TABLE games DROP COLUMN IF EXISTS rating_scored;

-- ========== 000029_notifications_cascade.down.sql ==========
-- 000029_notifications_cascade.down.sql
-- Р’РµСЂРЅСѓС‚СЊ РїСЂРµР¶РЅРёРµ FK Р±РµР· CASCADE (СЃ Р°РІС‚РѕРёРјРµРЅРѕРІР°РЅРёРµРј).
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_user_id_fkey;
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_game_id_fkey;
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_team_id_fkey;

ALTER TABLE notifications ADD CONSTRAINT notifications_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);
ALTER TABLE notifications ADD CONSTRAINT notifications_game_id_fkey FOREIGN KEY (game_id) REFERENCES games(id);
ALTER TABLE notifications ADD CONSTRAINT notifications_team_id_fkey FOREIGN KEY (team_id) REFERENCES teams(id);

-- ========== 000028_deferrable_level_position.down.sql ==========
-- 000028_deferrable_level_position.down.sql
-- Р’РѕР·РІСЂР°С‰Р°РµРј РѕР±С‹С‡РЅС‹Р№ (РЅРµ DEFERRABLE) unique-РєРѕРЅСЃС‚СЂРµР№РЅС‚ РЅР° (game_id, position).
-- РС‰РµРј Рё СѓРґР°Р»СЏРµРј constraint РїРѕ С„Р°РєС‚РёС‡РµСЃРєРѕРјСѓ РёРјРµРЅРё (C5).
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

-- ========== 000027_add_game_aggregates.down.sql ==========
-- 000027_add_game_aggregates.down.sql
DROP TRIGGER IF EXISTS trg_refresh_game_participants ON game_passings;
DROP TRIGGER IF EXISTS trg_refresh_game_rating ON reviews;
DROP FUNCTION IF EXISTS refresh_game_participants();
DROP FUNCTION IF EXISTS refresh_game_rating();
ALTER TABLE games DROP COLUMN IF EXISTS participant_count;
ALTER TABLE games DROP COLUMN IF EXISTS rating_value;

-- ========== 000026_add_refresh_token_family.down.sql ==========
-- 000026_add_refresh_token_family.down.sql
DROP INDEX IF EXISTS idx_refresh_tokens_family;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS family_id;

-- ========== 000025_add_tournament_scored.down.sql ==========
-- 000025_add_tournament_scored.down.sql
ALTER TABLE game_passings DROP COLUMN IF EXISTS tournament_scored;

-- ========== 000024_add_games_view.down.sql ==========
-- 000024_add_games_view.down.sql
ALTER TABLE users DROP COLUMN IF EXISTS games_view;

