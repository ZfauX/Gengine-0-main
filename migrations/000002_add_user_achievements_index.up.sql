-- 000002_add_user_achievements_index.up.sql
-- Добавление индекса на user_achievements.user_id для оптимизации запросов

CREATE INDEX IF NOT EXISTS idx_user_achievements_user_id ON user_achievements(user_id);

-- 000002_add_user_achievements_index.down.sql
-- Удаление индекса

DROP INDEX IF EXISTS idx_user_achievements_user_id;