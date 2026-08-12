-- 000067_chat_rooms_unique_remaining.down.sql
DROP INDEX IF EXISTS idx_chat_rooms_captains_unique;
DROP INDEX IF EXISTS idx_chat_rooms_team_flood_unique;
