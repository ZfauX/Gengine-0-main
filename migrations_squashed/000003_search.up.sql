-- 000003_search.up.sql
-- Полнотекстовый поиск (FTS + триграммы)

-- ========== FTS через tsvector ==========
ALTER TABLE games ADD COLUMN IF NOT EXISTS search_vector tsvector;

UPDATE games SET search_vector = to_tsvector('russian', COALESCE(name, '') || ' ' || COALESCE(description, ''));

CREATE INDEX IF NOT EXISTS idx_games_search_vector ON games USING GIN(search_vector);

CREATE OR REPLACE FUNCTION games_search_vector_update() RETURNS trigger AS $$
BEGIN
    NEW.search_vector := to_tsvector('russian', COALESCE(NEW.name, '') || ' ' || COALESCE(NEW.description, ''));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_games_search_vector ON games;
CREATE TRIGGER trg_games_search_vector
    BEFORE INSERT OR UPDATE OF name, description ON games
    FOR EACH ROW EXECUTE FUNCTION games_search_vector_update();

-- ========== Нечёткий поиск через pg_trgm ==========
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_games_name_trgm ON games USING gin (name gin_trgm_ops);
