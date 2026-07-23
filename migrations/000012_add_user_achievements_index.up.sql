-- 000012_add_user_achievements_index.up.sql
-- Добавление индекса на user_achievements.user_id для оптимизации запросов

CREATE INDEX IF NOT EXISTS idx_user_achievements_user_id ON user_achievements(user_id);