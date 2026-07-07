-- 000006_add_notification_settings.down.sql
-- Откат: удаление таблицы notification_settings

DROP INDEX IF EXISTS idx_notification_settings_user_id;
DROP TABLE IF EXISTS notification_settings;