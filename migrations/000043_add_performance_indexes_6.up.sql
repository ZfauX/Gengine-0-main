-- 000043_add_performance_indexes_6.up.sql
-- Индексы из pass 33 (секция B):

-- Список уведомлений пользователя: WHERE user_id ORDER BY created_at DESC.
-- idx_notifications_user_read (000017) покрывает фильтр по read, но не сортировку.
CREATE INDEX IF NOT EXISTS idx_notifications_user_created
    ON notifications(user_id, created_at DESC);

-- Листинг авторских игр: WHERE author_id AND is_draft ORDER BY created_at DESC
-- (дополняет idx_games_author_status 000005).
CREATE INDEX IF NOT EXISTS idx_games_author_draft_created
    ON games(author_id, is_draft, created_at DESC);
