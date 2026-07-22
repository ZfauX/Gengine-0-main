-- 000011_add_fts_index.up.sql
-- Добавляем tsvector колонку и GIN-индекс для полнотекстового поиска по играм
ALTER TABLE games ADD COLUMN IF NOT EXISTS search_vector tsvector;

-- Обновляем вектор из существующих данных
UPDATE games SET search_vector = to_tsvector('russian', COALESCE(name, '') || ' ' || COALESCE(description, ''));

-- Создаём GIN-индекс для быстрого полнотекстового поиска
CREATE INDEX IF NOT EXISTS idx_games_search_vector ON games USING GIN(search_vector);

-- Триггер для автоматического обновления search_vector при INSERT/UPDATE
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
