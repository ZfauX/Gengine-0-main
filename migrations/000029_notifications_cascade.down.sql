-- 000029_notifications_cascade.down.sql
-- Вернуть прежние FK без CASCADE (с автоименованием).
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_user_id_fkey;
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_game_id_fkey;
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_team_id_fkey;

ALTER TABLE notifications ADD CONSTRAINT notifications_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);
ALTER TABLE notifications ADD CONSTRAINT notifications_game_id_fkey FOREIGN KEY (game_id) REFERENCES games(id);
ALTER TABLE notifications ADD CONSTRAINT notifications_team_id_fkey FOREIGN KEY (team_id) REFERENCES teams(id);
