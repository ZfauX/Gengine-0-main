-- Добавление составного индекса для фильтрации игр по автору и статусу

CREATE INDEX IF NOT EXISTS idx_games_author_status ON games(author_id, is_draft);