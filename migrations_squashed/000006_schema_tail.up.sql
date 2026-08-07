-- migrations_squashed/000006_schema_tail.up.sql
-- Полный слепок изменений 000024-000036 (новые колонки, индексы, триггеры, constraints).
-- Created automatically from individual migrations to keep squashed schema current.

-- ========== 000024_add_games_view.up.sql ==========
-- 000024_add_games_view.up.sql
-- РџРµСЂСЃРѕРЅР°Р»СЊРЅРѕРµ РїСЂРµРґРїРѕС‡С‚РµРЅРёРµ: РІРёРґ СЃРїРёСЃРєР° РёРіСЂ (table | cards) РЅР° СЃРµСЂРІРµСЂРµ,
-- РІРјРµСЃС‚Рѕ localStorage РЅР° РєР»РёРµРЅС‚Рµ (СЃС‚СЂР°С‚РµРіРёС‡РµСЃРєР°СЏ РїРµСЂСЃРѕРЅР°Р»РёР·Р°С†РёСЏ).
ALTER TABLE users ADD COLUMN IF NOT EXISTS games_view varchar(10) NOT NULL DEFAULT 'table';

-- ========== 000025_add_tournament_scored.up.sql ==========
-- 000025_add_tournament_scored.up.sql
-- РРґРµРјРїРѕС‚РµРЅС‚РЅРѕСЃС‚СЊ РЅР°С‡РёСЃР»РµРЅРёСЏ С‚СѓСЂРЅРёСЂРЅС‹С… РѕС‡РєРѕРІ: РѕС‡РєРё РЅР°С‡РёСЃР»СЏСЋС‚СЃСЏ С‚РѕР»СЊРєРѕ
-- РѕРґРёРЅ СЂР°Р· Р·Р° РїСЂРѕС…РѕР¶РґРµРЅРёРµ (РѕР±С‹С‡РЅС‹Р№ С„РёРЅРёС€, С‚Р°Р№РјР°СѓС‚ РёР»Рё force-finish).
ALTER TABLE game_passings ADD COLUMN IF NOT EXISTS tournament_scored boolean NOT NULL DEFAULT false;

-- ========== 000026_add_refresh_token_family.up.sql ==========
-- 000026_add_refresh_token_family.up.sql
-- РЎРµРјСЊРё refresh-С‚РѕРєРµРЅРѕРІ: РїСЂРё reuse (РїРѕРІС‚РѕСЂРЅРѕРј РёСЃРїРѕР»СЊР·РѕРІР°РЅРёРё СѓР¶Рµ РѕС‚РѕР·РІР°РЅРЅРѕРіРѕ
-- С‚РѕРєРµРЅР°) РѕС‚Р·С‹РІР°РµС‚СЃСЏ РІСЃСЏ СЃРµРјСЊСЏ вЂ” Р·Р°С‰РёС‚Р° РѕС‚ РєСЂР°Р¶Рё СЃРµСЃСЃРёРё.
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS family_id varchar(64) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_family ON refresh_tokens(family_id);

-- ========== 000027_add_game_aggregates.up.sql ==========
-- 000027_add_game_aggregates.up.sql
-- РџСЂРµРєРѕРјРїСЊСЋС‚РёРЅРі Р°РіСЂРµРіР°С‚РѕРІ СЂРµР№С‚РёРЅРіР° Рё СѓС‡Р°СЃС‚РЅРёРєРѕРІ РЅР° games (P3):
-- Р»РёСЃС‚РёРЅРі /games Р±РѕР»СЊС€Рµ РЅРµ Р°РіСЂРµРіРёСЂСѓРµС‚ reviews/game_passings РЅР° РєР°Р¶РґС‹Р№ Р·Р°РїСЂРѕСЃ.

ALTER TABLE games ADD COLUMN IF NOT EXISTS rating_value double precision NOT NULL DEFAULT 0;
ALTER TABLE games ADD COLUMN IF NOT EXISTS participant_count integer NOT NULL DEFAULT 0;

-- Backfill СЃСѓС‰РµСЃС‚РІСѓСЋС‰РёС… РґР°РЅРЅС‹С…
UPDATE games g
SET rating_value = COALESCE((SELECT AVG(r.rating) FROM reviews r WHERE r.game_id = g.id), 0);

UPDATE games g
SET participant_count = (
    SELECT COUNT(DISTINCT gp.team_id) FROM game_passings gp
    WHERE gp.game_id = g.id AND gp.status IN ('accepted','started','finished')
);

-- РўСЂРёРіРіРµСЂС‹: РѕР±РЅРѕРІР»РµРЅРёРµ РєРѕР»РѕРЅРѕРє РїСЂРё РёР·РјРµРЅРµРЅРёРё reviews / game_passings
CREATE OR REPLACE FUNCTION refresh_game_rating() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        UPDATE games SET rating_value = COALESCE((SELECT AVG(r.rating) FROM reviews r WHERE r.game_id = OLD.game_id), 0)
        WHERE id = OLD.game_id;
    ELSE
        UPDATE games SET rating_value = COALESCE((SELECT AVG(r.rating) FROM reviews r WHERE r.game_id = NEW.game_id), 0)
        WHERE id = NEW.game_id;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_refresh_game_rating ON reviews;
CREATE TRIGGER trg_refresh_game_rating
AFTER INSERT OR UPDATE OR DELETE ON reviews
FOR EACH ROW EXECUTE FUNCTION refresh_game_rating();

CREATE OR REPLACE FUNCTION refresh_game_participants() RETURNS trigger AS $$
DECLARE
    gid bigint;
BEGIN
    gid := COALESCE(NEW.game_id, OLD.game_id);
    UPDATE games SET participant_count = (
        SELECT COUNT(DISTINCT gp.team_id) FROM game_passings gp
        WHERE gp.game_id = gid AND gp.status IN ('accepted','started','finished')
    ) WHERE id = gid;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_refresh_game_participants ON game_passings;
CREATE TRIGGER trg_refresh_game_participants
AFTER INSERT OR UPDATE OR DELETE ON game_passings
FOR EACH ROW EXECUTE FUNCTION refresh_game_participants();

-- ========== 000028_deferrable_level_position.up.sql ==========
-- 000028_deferrable_level_position.up.sql
-- РЈРЅРёРєР°Р»СЊРЅРѕСЃС‚СЊ (game_id, position) СЃС‚Р°РЅРѕРІРёС‚СЃСЏ DEFERRABLE INITIALLY DEFERRED:
-- РїРµСЂРµРјРµС‰РµРЅРёСЏ/СЃРґРІРёРіРё РїРѕР·РёС†РёР№ СѓСЂРѕРІРЅРµР№ (Move/Duplicate) РЅРµ РїР°РґР°СЋС‚ РЅР° С‚СЂР°РЅР·РёРµРЅС‚РЅС‹С…
-- РєРѕР»Р»РёР·РёСЏС… РІРЅСѓС‚СЂРё РѕРґРЅРѕР№ С‚СЂР°РЅР·Р°РєС†РёРё вЂ” РїСЂРѕРІРµСЂРєР° РІС‹РїРѕР»РЅСЏРµС‚СЃСЏ РїСЂРё COMMIT (B7/B8).
--
-- DROP РґРµР»Р°РµРј РїРѕ С„Р°РєС‚РёС‡РµСЃРєРѕРјСѓ РёРјРµРЅРё constraint'Р° (pg_constraint), Р° РЅРµ РїРѕ
-- РїСЂРµРґРїРѕР»РѕР¶РµРЅРёСЋ levels_game_id_position_key: GORM РјРѕР¶РµС‚ СЃРѕР·РґР°С‚СЊ СЃРІРѕС‘ РёРјСЏ
-- (C5) вЂ” РёРЅР°С‡Рµ DROP Р±С‹Р» no-op, Р° ADD РґСѓР±Р»РёСЂРѕРІР°Р» Р±С‹ РѕРіСЂР°РЅРёС‡РµРЅРёРµ.

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
      AND conkey::int[] = ARRAY[game_id_attnum, position_attnum]
    ORDER BY conname LIMIT 1;

    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE levels DROP CONSTRAINT %I', constraint_name);
    END IF;
END $$;

ALTER TABLE levels ADD CONSTRAINT levels_game_id_position_key
    UNIQUE (game_id, position) DEFERRABLE INITIALLY DEFERRED;

-- ========== 000029_notifications_cascade.up.sql ==========
-- 000029_notifications_cascade.up.sql
-- ON DELETE CASCADE РґР»СЏ СѓРІРµРґРѕРјР»РµРЅРёР№: Р¶С‘СЃС‚РєРѕРµ СѓРґР°Р»РµРЅРёРµ РїРѕР»СЊР·РѕРІР°С‚РµР»СЏ/РёРіСЂС‹/РєРѕРјР°РЅРґС‹
-- Р±РѕР»СЊС€Рµ РЅРµ РѕСЃС‚Р°РІР»СЏРµС‚ РѕСЃРёСЂРѕС‚РµРІС€РёРµ СѓРІРµРґРѕРјР»РµРЅРёСЏ (B5 вЂ” РѕС‡РёСЃС‚РєР° Р·Р°РІРёСЃРёРјРѕСЃС‚РµР№).

DO $$
DECLARE
    constraint_name TEXT;
BEGIN
    -- user_id в†’ users
    SELECT conname INTO constraint_name FROM pg_constraint
    WHERE conrelid = 'notifications'::regclass AND contype = 'f' AND confrelid = 'users'::regclass
    LIMIT 1;
    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE notifications DROP CONSTRAINT %I', constraint_name);
    END IF;
    ALTER TABLE notifications ADD CONSTRAINT notifications_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

    -- game_id в†’ games
    SELECT conname INTO constraint_name FROM pg_constraint
    WHERE conrelid = 'notifications'::regclass AND contype = 'f' AND confrelid = 'games'::regclass
    LIMIT 1;
    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE notifications DROP CONSTRAINT %I', constraint_name);
    END IF;
    ALTER TABLE notifications ADD CONSTRAINT notifications_game_id_fkey
        FOREIGN KEY (game_id) REFERENCES games(id) ON DELETE CASCADE;

    -- team_id в†’ teams
    SELECT conname INTO constraint_name FROM pg_constraint
    WHERE conrelid = 'notifications'::regclass AND contype = 'f' AND confrelid = 'teams'::regclass
    LIMIT 1;
    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE notifications DROP CONSTRAINT %I', constraint_name);
    END IF;
    ALTER TABLE notifications ADD CONSTRAINT notifications_team_id_fkey
        FOREIGN KEY (team_id) REFERENCES teams(id) ON DELETE CASCADE;
END $$;

-- ========== 000030_add_rating_scored.up.sql ==========
-- 000030_add_rating_scored.up.sql
-- РРґРµРјРїРѕС‚РµРЅС‚РЅРѕСЃС‚СЊ РЅР°С‡РёСЃР»РµРЅРёСЏ РѕС‡РєРѕРІ СЂРµР№С‚РёРЅРіР° (B3): С„Р»Р°Рі РЅР° games РїСЂРµРґРѕС‚РІСЂР°С‰Р°РµС‚
-- РґРІРѕР№РЅРѕРµ РЅР°С‡РёСЃР»РµРЅРёРµ РїСЂРё РїРѕРІС‚РѕСЂРЅРѕРј РІС‹Р·РѕРІРµ UpdateRatingsForGame (Р°РІС‚РѕСЂСЃРєРёРµ +
-- РєРѕРјР°РЅРґРЅС‹Рµ РѕС‡РєРё РЅР°С‡РёСЃР»СЏСЋС‚СЃСЏ РѕРґРёРЅ СЂР°Р· Р·Р° РёРіСЂСѓ).
ALTER TABLE games ADD COLUMN IF NOT EXISTS rating_scored boolean NOT NULL DEFAULT false;

-- ========== 000031_voting_sessions_unique.up.sql ==========
-- 000031_voting_sessions_unique.up.sql
-- РЈРЅРёРєР°Р»СЊРЅС‹Р№ РёРЅРґРµРєСЃ (РїСЂРѕС…РѕР¶РґРµРЅРёРµ, СѓСЂРѕРІРµРЅСЊ) РґР»СЏ СЃРµСЃСЃРёР№ РіРѕР»РѕСЃРѕРІР°РЅРёСЏ.
-- Р—Р°РєСЂС‹РІР°РµС‚ check-then-insert РіРѕРЅРєСѓ РІ StartVoting (B8): РґРІР° РїР°СЂР°Р»Р»РµР»СЊРЅС‹С…
-- РѕС‚РєСЂС‹С‚РёСЏ РЅРµ СЃРѕР·РґР°РґСѓС‚ РґРІРµ СЃРµСЃСЃРёРё вЂ” РІС‚РѕСЂРѕР№ INSERT РїРѕР»СѓС‡РёС‚ unique violation.
-- РРјСЏ СЃРѕРІРїР°РґР°РµС‚ СЃ GORM-С‚РµРіРѕРј РјРѕРґРµР»Рё (uniqueIndex:idx_passing_level), С‡С‚РѕР±С‹
-- С‚РµСЃС‚С‹ (AutoMigrate) Рё РїСЂРѕРґР°РєС€РЅ-РјРёРіСЂР°С†РёРё РЅРµ СЂР°СЃС…РѕРґРёР»РёСЃСЊ.
CREATE UNIQUE INDEX IF NOT EXISTS idx_passing_level
    ON blackbox_voting_sessions(game_passing_id, level_id);

-- ========== 000032_notifications_user_created.up.sql ==========
-- 000032_notifications_user_created.up.sql
-- РЎРѕСЂС‚РёСЂРѕРІРєР° СЃРїРёСЃРєР° СѓРІРµРґРѕРјР»РµРЅРёР№ РїРѕ created_at DESC (P6): СЃСѓС‰РµСЃС‚РІСѓСЋС‰РёР№ РёРЅРґРµРєСЃ
-- (user_id, read) РЅРµ РїРѕРєСЂС‹РІР°РµС‚ СЃРѕСЂС‚РёСЂРѕРІРєСѓ вЂ” РїСЂРё Р±РѕР»СЊС€РѕРј РѕР±СЉС‘РјРµ СѓРІРµРґРѕРјР»РµРЅРёР№
-- РїР°РіРёРЅР°С†РёСЏ С‚СЂРµР±СѓРµС‚ filesort.
CREATE INDEX IF NOT EXISTS idx_notifications_user_created
    ON notifications(user_id, created_at DESC);

-- ========== 000033_add_tournament_points.up.sql ==========
-- 000033_add_tournament_points.up.sql
-- РўРѕС‡РЅРѕРµ Р·РЅР°С‡РµРЅРёРµ РЅР°С‡РёСЃР»РµРЅРЅС‹С… С‚СѓСЂРЅРёСЂРЅС‹С… РѕС‡РєРѕРІ РЅР° РїСЂРѕС…РѕР¶РґРµРЅРёРё (C-M2):
-- RemoveGame СЃРїРёСЃС‹РІР°РµС‚ СЂРѕРІРЅРѕ С‚Рѕ, С‡С‚Рѕ Р±С‹Р»Рѕ РЅР°С‡РёСЃР»РµРЅРѕ РїСЂРё РїРѕРґСЃС‡С‘С‚Рµ, Р° РЅРµ
-- РїРµСЂРµСЃС‡РёС‚С‹РІР°РµС‚ РїРѕ С‚РµРєСѓС‰РµРјСѓ РјРµСЃС‚Сѓ/РЅР°СЃС‚СЂРѕР№РєР°Рј (РєРѕС‚РѕСЂС‹Рµ РјРѕРіР»Рё РёР·РјРµРЅРёС‚СЊСЃСЏ).
ALTER TABLE game_passings ADD COLUMN IF NOT EXISTS tournament_points integer NOT NULL DEFAULT 0;

-- ========== 000034_unique_reviews.up.sql ==========
-- 000034_unique_reviews.up.sql
-- Р—Р°С‰РёС‚Р° РѕС‚ РґСѓР±Р»РёСЂРѕРІР°РЅРёСЏ РѕС‚Р·С‹РІРѕРІ (pass 24 / C-3): СѓРЅРёРєР°Р»СЊРЅС‹Р№ РёРЅРґРµРєСЃ
-- reviews(game_id, user_id) вЂ” РґРІР° РїР°СЂР°Р»Р»РµР»СЊРЅС‹С… POST РѕС‚ РѕРґРЅРѕРіРѕ РїРѕР»СЊР·РѕРІР°С‚РµР»СЏ
-- РЅРµ СЃРѕР·РґР°РґСѓС‚ РґРІР° РѕС‚Р·С‹РІР° Рё РЅРµ Р·Р°РІС‹СЃСЏС‚ СЂРµР№С‚РёРЅРі.
CREATE UNIQUE INDEX IF NOT EXISTS idx_reviews_game_user ON reviews(game_id, user_id);

-- ========== 000035_add_sort_and_search_indexes.up.sql ==========
-- 000035_add_sort_and_search_indexes.up.sql
-- РРЅРґРµРєСЃС‹ РґР»СЏ СЃРѕСЂС‚РёСЂРѕРІРєРё Р»РёСЃС‚РёРЅРіР° РёРіСЂ РїРѕ РїСЂРµРєРѕРјРїСЊСЋС‚РёСЂРѕРІР°РЅРЅС‹Рј Р°РіСЂРµРіР°С‚Р°Рј (P-7)
-- Рё РґР»СЏ LOWER() LIKE-РїРѕРёСЃРєР° РїРѕР»СЊР·РѕРІР°С‚РµР»РµР№ (P-8, trgm РёРЅРґРµРєСЃС‹ 000023 вЂ” case-sensitive).
CREATE INDEX IF NOT EXISTS idx_games_rating_value
    ON games(is_draft, visibility, rating_value DESC);
CREATE INDEX IF NOT EXISTS idx_games_participant_count
    ON games(is_draft, visibility, participant_count DESC);

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_users_name_lower_trgm
    ON users USING gin (lower(name) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_users_email_lower_trgm
    ON users USING gin (lower(email) gin_trgm_ops);

-- ========== 000036_add_games_name_trgm.up.sql ==========
-- 000036_add_games_name_trgm.up.sql
-- pg_trgm-РёРЅРґРµРєСЃ РЅР° games.name РґР»СЏ ILIKE-РїРѕРёСЃРєР° (pass 26 / autocomplete + admin):
-- Р±РµР· РЅРµРіРѕ `%..%` РїРѕРёСЃРє СЃРєР°РЅРёСЂСѓРµС‚ РІСЃСЋ С‚Р°Р±Р»РёС†Сѓ.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_games_name_trgm ON games USING gin (name gin_trgm_ops);
