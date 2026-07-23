-- 000012_add_user_achievements_index.down.sql
-- Удаление индекса

DROP INDEX IF EXISTS idx_user_achievements_user_id;