-- 000029_notifications_cascade.up.sql
-- ON DELETE CASCADE для уведомлений: жёсткое удаление пользователя/игры/команды
-- больше не оставляет осиротевшие уведомления (B5 — очистка зависимостей).

DO $$
DECLARE
    constraint_name TEXT;
BEGIN
    -- user_id → users
    SELECT conname INTO constraint_name FROM pg_constraint
    WHERE conrelid = 'notifications'::regclass AND contype = 'f' AND confrelid = 'users'::regclass
    LIMIT 1;
    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE notifications DROP CONSTRAINT %I', constraint_name);
    END IF;
    ALTER TABLE notifications ADD CONSTRAINT notifications_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

    -- game_id → games
    SELECT conname INTO constraint_name FROM pg_constraint
    WHERE conrelid = 'notifications'::regclass AND contype = 'f' AND confrelid = 'games'::regclass
    LIMIT 1;
    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE notifications DROP CONSTRAINT %I', constraint_name);
    END IF;
    ALTER TABLE notifications ADD CONSTRAINT notifications_game_id_fkey
        FOREIGN KEY (game_id) REFERENCES games(id) ON DELETE CASCADE;

    -- team_id → teams
    SELECT conname INTO constraint_name FROM pg_constraint
    WHERE conrelid = 'notifications'::regclass AND contype = 'f' AND confrelid = 'teams'::regclass
    LIMIT 1;
    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE notifications DROP CONSTRAINT %I', constraint_name);
    END IF;
    ALTER TABLE notifications ADD CONSTRAINT notifications_team_id_fkey
        FOREIGN KEY (team_id) REFERENCES teams(id) ON DELETE CASCADE;
END $$;
