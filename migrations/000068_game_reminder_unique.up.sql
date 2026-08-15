-- 000068_game_reminder_unique.up.sql
-- M6 (PASS-16): дедупликация напоминаний о предстоящих играх.
-- Раньше фоновый воркер (main.go, раз в час) создавал уведомления для игр,
-- стартующих через 30/14/7/1 день, без проверки существования — при сдвиге
-- даты старта игра попадала в выборку несколько часов подряд, и пользователь
-- получал дубликаты. Уникальный частичный индекс гарантирует максимум ОДНО
-- напоминание (user_id, game_id) типа 'game_reminder'.
CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_game_reminder_unique
    ON notifications(user_id, game_id)
    WHERE type = 'game_reminder' AND deleted_at IS NULL;
