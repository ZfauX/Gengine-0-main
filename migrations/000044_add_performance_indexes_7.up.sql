-- 000044_add_performance_indexes_7.up.sql
-- Индексы из pass 34 (секция B):

-- Retention прочитанных уведомлений: ежедневный DELETE по read_at.
-- Без partial-индекса — полный seq-скан таблицы (F-2).
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_notifications_read_at_read
    ON notifications(read_at) WHERE read = true;

-- Аутентифицированный листинг игр: OR-ветка (visibility/public/author).
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_games_visibility_draft_author
    ON games(visibility, is_draft, author_id);
