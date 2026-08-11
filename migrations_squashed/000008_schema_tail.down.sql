-- migrations_squashed/000008_schema_tail.down.sql

-- ========== 000061_team_members_user_unique.down.sql ==========
-- 000061_team_members_user_unique.down.sql
DROP INDEX IF EXISTS idx_team_members_user_unique;

-- ========== 000060_tournament_scored_ids.down.sql ==========
-- 000060_tournament_scored_ids.down.sql
ALTER TABLE game_passings DROP COLUMN IF EXISTS tournament_scored_ids;

