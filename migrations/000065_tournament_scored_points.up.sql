-- 000065_tournament_scored_points.up.sql
-- DEEP-REVIEW PASS-6 M4: параллельный массив начисленных очков турнира.
-- Индекс элемента соответствует tournament_scored_ids: RemoveGame списывает
-- ТОЧНО начисленное значение, даже если автор изменил PointsFor* между
-- финишем и удалением игры (раньше пересчитывался по текущей конфигурации).
ALTER TABLE game_passings ADD COLUMN IF NOT EXISTS tournament_scored_points BIGINT[] NOT NULL DEFAULT '{}';
