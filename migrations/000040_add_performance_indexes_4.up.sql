-- 000040_add_performance_indexes_4.up.sql
-- Индексы из pass 31 (секция B):

-- Аудит: фильтр user_id/action + пагинация ORDER BY created_at DESC.
-- Одна большая append-only таблица — составные индексы вместо пересечения
-- одноколоночных (F2).
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_created
    ON audit_logs(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_logs_action_created
    ON audit_logs(action, created_at DESC);

-- Dashboard: pending-приглашения пользователя с сортировкой по времени.
-- idx_invitations_user_status покрывает фильтр, но не сортировку (F12).
CREATE INDEX IF NOT EXISTS idx_invitations_user_status_created
    ON invitations(user_id, status, created_at DESC);
