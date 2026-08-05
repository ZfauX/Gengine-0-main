-- 000031_voting_sessions_unique.up.sql
-- Уникальный индекс (прохождение, уровень) для сессий голосования.
-- Закрывает check-then-insert гонку в StartVoting (B8): два параллельных
-- открытия не создадут две сессии — второй INSERT получит unique violation.
-- Имя совпадает с GORM-тегом модели (uniqueIndex:idx_passing_level), чтобы
-- тесты (AutoMigrate) и продакшн-миграции не расходились.
CREATE UNIQUE INDEX IF NOT EXISTS idx_passing_level
    ON blackbox_voting_sessions(game_passing_id, level_id);
