-- 000056_notify_game_days.up.sql
-- D-1 (pass 45): за сколько дней уведомлять пользователя о предстоящих играх.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS notify_game_days INTEGER NOT NULL DEFAULT 0;
