-- 000065_tournament_scored_points.up.sql
-- DEEP-REVIEW PASS-6 M4: параллельный массив начисленных очков турнира.
-- Индекс элемента соответствует tournament_scored_ids: RemoveGame списывает
-- ТОЧНО начисленное значение, даже если автор изменил PointsFor* между
-- финишем и удалением игры (раньше пересчитывался по текущей конфигурации).
ALTER TABLE game_passings ADD COLUMN IF NOT EXISTS tournament_scored_points BIGINT[] NOT NULL DEFAULT '{}';

-- M3 (PASS-7): backfill для строк, созданных ДО этой миграции.
-- У них tournament_scored_ids уже заполнен, а tournament_scored_points пуст —
-- RemoveGame списал бы 0 вместо начисленных очков.
-- Точные значения по отдельным турнирам для legacy-строк не сохранились,
-- поэтому раскладываем суммарный tournament_points по элементам (base + остаток
-- в последний элемент, сумма сохраняется). Новые начисления пишут точные значения.
UPDATE game_passings
SET tournament_scored_points = (
    SELECT array_agg(v ORDER BY o)
    FROM (
        SELECT o,
               CASE WHEN o = (SELECT max(o) FROM unnest(tournament_scored_ids) WITH ORDINALITY AS x(id, o))
                    THEN tournament_points
                         - (cardinality(tournament_scored_ids) - 1) * (tournament_points / cardinality(tournament_scored_ids))
                    ELSE tournament_points / cardinality(tournament_scored_ids)
               END AS v
        FROM unnest(tournament_scored_ids) WITH ORDINALITY AS y(id, o)
    ) sub
)
WHERE cardinality(tournament_scored_ids) > 0
  AND cardinality(tournament_scored_points) = 0
  AND tournament_points > 0;
