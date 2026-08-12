-- migrations_squashed/000012_schema_tail.up.sql
-- Дополнение после миграции 000065.

-- ========== 000065_tournament_scored_points.up.sql ==========
ALTER TABLE game_passings ADD COLUMN IF NOT EXISTS tournament_scored_points BIGINT[] NOT NULL DEFAULT '{}';
