-- 000027_add_game_aggregates.up.sql
-- Прекомпьютинг агрегатов рейтинга и участников на games (P3):
-- листинг /games больше не агрегирует reviews/game_passings на каждый запрос.

ALTER TABLE games ADD COLUMN IF NOT EXISTS rating_value double precision NOT NULL DEFAULT 0;
ALTER TABLE games ADD COLUMN IF NOT EXISTS participant_count integer NOT NULL DEFAULT 0;

-- Backfill существующих данных
UPDATE games g
SET rating_value = COALESCE((SELECT AVG(r.rating) FROM reviews r WHERE r.game_id = g.id), 0);

UPDATE games g
SET participant_count = (
    SELECT COUNT(DISTINCT gp.team_id) FROM game_passings gp
    WHERE gp.game_id = g.id AND gp.status IN ('accepted','started','finished')
);

-- Триггеры: обновление колонок при изменении reviews / game_passings
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
