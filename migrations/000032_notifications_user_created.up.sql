-- 000032_notifications_user_created.up.sql
-- Сортировка списка уведомлений по created_at DESC (P6): существующий индекс
-- (user_id, read) не покрывает сортировку — при большом объёме уведомлений
-- пагинация требует filesort.
CREATE INDEX IF NOT EXISTS idx_notifications_user_created
    ON notifications(user_id, created_at DESC);
